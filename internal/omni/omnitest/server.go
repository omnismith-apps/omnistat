// Package omnitest is an in-memory fake of the subset of the Omnismith API that
// omnistat uses, served over httptest so tests exercise the real SDK. It speaks
// the JSON shapes of the OpenAPI contract (v1.0.x) closely enough for the SDK's
// generated models, enforces the auth and project headers, and offers fault
// injection for race and failure tests.
package omnitest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Defaults used by New.
const (
	Token     = "omni_test_token"
	ProjectID = "01a0c47a-0000-7000-8000-000000000001"
)

// Request is one recorded request.
type Request struct {
	Method string
	Path   string
	Body   string
}

// Fault is an injected failure for the next matching request.
type Fault struct {
	// Method/PathPrefix select the request; empty matches anything.
	Method, PathPrefix string
	Status             int
	Body               string
	// Times is how many matching requests fail (default 1).
	Times int
}

// Server is the fake API.
type Server struct {
	*httptest.Server

	mu          sync.Mutex
	token       string
	projectID   string
	permissions []string
	nextID      int
	templates   []*template
	attributes  []*attribute
	entities    []*entity
	requests    []Request
	faults      []Fault
	// Before runs under the lock before every authenticated request; tests use
	// it to simulate another actor (fleet race) or to observe traffic.
	Before func(r *http.Request)
	// DenyWrites answers every mutating request with 403.
	DenyWrites bool
}

type template struct {
	ID, Slug, Name, Description string
	AttributeIDs                []string
}

type attribute struct {
	ID, Slug, Name, Description string
	AttributeType, DataType     int
	Options                     []listItem
}

type listItem struct{ ID, Value string }

type entity struct {
	ID, TemplateID string
	Values         map[string]any // attribute slug → value
	CreatedAt      string
}

// New starts a fake server with the default token, project id and full permissions.
func New() *Server {
	s := &Server{token: Token, projectID: ProjectID, permissions: []string{"schema.write", "entity.write"}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// Requests returns a copy of every request seen so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// FailNext queues a fault.
func (s *Server) FailNext(f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.Times == 0 {
		f.Times = 1
	}
	s.faults = append(s.faults, f)
}

// SetPermissions replaces the permission strings returned by /auth/me/permissions.
func (s *Server) SetPermissions(p ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permissions = p
}

// --- seeding & inspection (safe to call from tests and Before hooks) -------

// AddTemplate seeds a template bound to the given attribute slugs and returns its id.
// Callable from a Before hook (lock already held) via addTemplateLocked.
func (s *Server) AddTemplate(slug, name string, attrSlugs ...string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addTemplateLocked(slug, name, "", attrSlugs)
}

// AddTemplateLocked is AddTemplate for use inside Before hooks.
func (s *Server) AddTemplateLocked(slug, name string, attrSlugs ...string) string {
	return s.addTemplateLocked(slug, name, "", attrSlugs)
}

func (s *Server) addTemplateLocked(slug, name, desc string, attrSlugs []string) string {
	t := &template{ID: s.id(), Slug: slug, Name: name, Description: desc}
	for _, as := range attrSlugs {
		if a := s.attrBySlug(as); a != nil {
			t.AttributeIDs = append(t.AttributeIDs, a.ID)
		}
	}
	s.templates = append(s.templates, t)
	return t.ID
}

// AddAttribute seeds an attribute of the given semantic type ("string",
// "number", "boolean", "datetime", "date", "list", "metric", "reference") with
// optional list options and returns its id.
func (s *Server) AddAttribute(slug, typ string, options ...string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addAttributeLocked(slug, typ, options)
}

// AddAttributeLocked is AddAttribute for use inside Before hooks.
func (s *Server) AddAttributeLocked(slug, typ string, options ...string) string {
	return s.addAttributeLocked(slug, typ, options)
}

func (s *Server) addAttributeLocked(slug, typ string, options []string) string {
	at, dt := typeToEnums(typ)
	a := &attribute{ID: s.id(), Slug: slug, Name: strings.ToUpper(slug[:1]) + slug[1:], AttributeType: at, DataType: dt}
	for _, o := range options {
		a.Options = append(a.Options, listItem{ID: s.id(), Value: o})
	}
	s.attributes = append(s.attributes, a)
	return a.ID
}

// Bind adds an existing attribute to an existing template (by slug).
func (s *Server) Bind(templateSlug, attrSlug string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, a := s.tplBySlug(templateSlug), s.attrBySlug(attrSlug)
	if t == nil || a == nil {
		panic(fmt.Sprintf("omnitest.Bind: unknown %q/%q", templateSlug, attrSlug))
	}
	t.AttributeIDs = appendUnique(t.AttributeIDs, a.ID)
}

// TemplateSnapshot is a read-only view for assertions.
type TemplateSnapshot struct {
	ID, Slug, Name string
	AttributeSlugs []string
}

// AttributeSnapshot is a read-only view for assertions.
type AttributeSnapshot struct {
	ID, Slug, Name, Type string
	Options              []string
}

// Templates returns all templates, in creation order.
func (s *Server) Templates() []TemplateSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []TemplateSnapshot
	for _, t := range s.templates {
		snap := TemplateSnapshot{ID: t.ID, Slug: t.Slug, Name: t.Name}
		for _, id := range t.AttributeIDs {
			if a := s.attrByID(id); a != nil {
				snap.AttributeSlugs = append(snap.AttributeSlugs, a.Slug)
			}
		}
		out = append(out, snap)
	}
	return out
}

// Attributes returns all attributes, in creation order.
func (s *Server) Attributes() []AttributeSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AttributeSnapshot
	for _, a := range s.attributes {
		snap := AttributeSnapshot{ID: a.ID, Slug: a.Slug, Name: a.Name, Type: enumsToType(a.AttributeType, a.DataType)}
		for _, o := range a.Options {
			snap.Options = append(snap.Options, o.Value)
		}
		out = append(out, snap)
	}
	return out
}

// --- HTTP ------------------------------------------------------------------

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{r.Method, r.URL.Path, string(body)})

	if r.Header.Get("Authorization") != "Bearer "+s.token {
		problem(w, 401, "Unauthorized", "invalid or missing token", "")
		return
	}
	if r.URL.Path != "/auth/me/permissions" {
		switch pid := r.Header.Get("X-Omnismith-Project-Id"); {
		case pid == "":
			problem(w, 409, "No project selected", "select a project", "no_project_selected")
			return
		case pid != s.projectID:
			problem(w, 403, "Forbidden", "not a member of this project", "")
			return
		}
	}
	if s.Before != nil {
		s.Before(r)
	}
	for i, f := range s.faults {
		if (f.Method == "" || f.Method == r.Method) && strings.HasPrefix(r.URL.Path, f.PathPrefix) {
			f.Times--
			if f.Times == 0 {
				s.faults = append(s.faults[:i], s.faults[i+1:]...)
			} else {
				s.faults[i] = f
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.Status)
			if f.Body != "" {
				_, _ = io.WriteString(w, f.Body)
			} else {
				_, _ = fmt.Fprintf(w, `{"type":"error/server","title":"Injected","status":%d,"detail":"injected fault"}`, f.Status)
			}
			return
		}
	}
	if s.DenyWrites && r.Method != http.MethodGet {
		problem(w, 403, "Forbidden", "missing permission", "")
		return
	}

	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == "GET" && path == "/discovery/project-schema":
		s.discovery(w)
	case r.Method == "GET" && path == "/auth/me/permissions":
		writeJSON(w, 200, map[string]any{"data": s.permissions})
	case r.Method == "POST" && path == "/templates":
		s.createTemplate(w, body)
	case r.Method == "POST" && path == "/attributes":
		s.createAttribute(w, body)
	case r.Method == "POST" && strings.HasPrefix(path, "/attributes/") && strings.HasSuffix(path, "/items"):
		s.createItem(w, strings.TrimSuffix(strings.TrimPrefix(path, "/attributes/"), "/items"), body)
	case r.Method == "PATCH" && strings.HasPrefix(path, "/attributes/"):
		s.patchAttribute(w, strings.TrimPrefix(path, "/attributes/"), body)
	case r.Method == "POST" && strings.HasPrefix(path, "/entities/search/"):
		s.searchEntities(w, strings.TrimPrefix(path, "/entities/search/"), body)
	case r.Method == "POST" && strings.HasPrefix(path, "/entities/template/"):
		s.createEntity(w, strings.TrimPrefix(path, "/entities/template/"), body)
	default:
		problem(w, 404, "Not Found", "no such route in omnitest: "+r.Method+" "+path, "")
	}
}

func (s *Server) discovery(w http.ResponseWriter) {
	tpls := []map[string]any{}
	for _, t := range s.templates {
		attrs := []map[string]any{}
		for _, id := range t.AttributeIDs {
			if a := s.attrByID(id); a != nil {
				attrs = append(attrs, map[string]any{"id": a.ID, "slug": a.Slug})
			}
		}
		tpls = append(tpls, map[string]any{
			"id": t.ID, "slug": t.Slug, "name": t.Name, "description": t.Description,
			"attributes": attrs, "rules": []any{}, "actions": []any{},
		})
	}
	attrs := []map[string]any{}
	for _, a := range s.attributes {
		m := map[string]any{"id": a.ID, "slug": a.Slug, "name": a.Name, "type": enumsToType(a.AttributeType, a.DataType), "description": a.Description}
		if a.AttributeType == 2 {
			opts := []map[string]any{}
			for _, o := range a.Options {
				opts = append(opts, map[string]any{"id": o.ID, "value": o.Value})
			}
			m["options"] = opts
		}
		attrs = append(attrs, m)
	}
	writeJSON(w, 200, map[string]any{"project_id": s.projectID, "project_name": "omnitest", "templates": tpls, "attributes": attrs})
}

func (s *Server) createTemplate(w http.ResponseWriter, body []byte) {
	var req struct {
		Name           string   `json:"name"`
		Slug           *string  `json:"slug"`
		Description    *string  `json:"description"`
		AttributeIDs   []string `json:"attribute_ids"`
		AttributeSlugs []string `json:"attribute_slugs"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Name == "" {
		problem(w, 400, "Bad Request", "invalid payload", "")
		return
	}
	slug := slugify(req.Name)
	if req.Slug != nil {
		slug = *req.Slug
	}
	if s.tplBySlug(slug) != nil {
		validation(w, map[string][]string{"slug": {"The slug has already been taken."}})
		return
	}
	t := &template{ID: s.id(), Slug: slug, Name: req.Name}
	if req.Description != nil {
		t.Description = *req.Description
	}
	t.AttributeIDs = append(t.AttributeIDs, req.AttributeIDs...)
	for _, as := range req.AttributeSlugs {
		if a := s.attrBySlug(as); a != nil {
			t.AttributeIDs = appendUnique(t.AttributeIDs, a.ID)
		}
	}
	s.templates = append(s.templates, t)
	writeJSON(w, 201, map[string]any{"id": t.ID})
}

func (s *Server) createAttribute(w http.ResponseWriter, body []byte) {
	var req struct {
		Name          string   `json:"name"`
		Slug          *string  `json:"slug"`
		Description   *string  `json:"description"`
		AttributeType *int     `json:"attribute_type"`
		DataType      *int     `json:"data_type"`
		TemplateIDs   []string `json:"template_ids"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Name == "" || req.AttributeType == nil || req.DataType == nil {
		problem(w, 400, "Bad Request", "invalid payload", "")
		return
	}
	slug := slugify(req.Name)
	if req.Slug != nil {
		slug = *req.Slug
	}
	if s.attrBySlug(slug) != nil {
		validation(w, map[string][]string{"slug": {"The slug has already been taken."}})
		return
	}
	for _, id := range req.TemplateIDs {
		if s.tplByID(id) == nil {
			validation(w, map[string][]string{"template_ids": {"Unknown template " + id}})
			return
		}
	}
	a := &attribute{ID: s.id(), Slug: slug, Name: req.Name, AttributeType: *req.AttributeType, DataType: *req.DataType}
	if req.Description != nil {
		a.Description = *req.Description
	}
	s.attributes = append(s.attributes, a)
	for _, id := range req.TemplateIDs {
		t := s.tplByID(id)
		t.AttributeIDs = appendUnique(t.AttributeIDs, a.ID)
	}
	writeJSON(w, 201, map[string]any{"id": a.ID})
}

func (s *Server) createItem(w http.ResponseWriter, attrID string, body []byte) {
	a := s.attrByID(attrID)
	if a == nil {
		problem(w, 404, "Not Found", "attribute not found", "")
		return
	}
	if a.AttributeType != 2 {
		validation(w, map[string][]string{"attribute": {"Not a list attribute."}})
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Value == "" {
		problem(w, 400, "Bad Request", "invalid payload", "")
		return
	}
	for _, o := range a.Options {
		if o.Value == req.Value {
			validation(w, map[string][]string{"value": {"The value has already been taken."}})
			return
		}
	}
	it := listItem{ID: s.id(), Value: req.Value}
	a.Options = append(a.Options, it)
	writeJSON(w, 201, map[string]any{"id": it.ID})
}

func (s *Server) patchAttribute(w http.ResponseWriter, attrID string, body []byte) {
	a := s.attrByID(attrID)
	if a == nil {
		problem(w, 404, "Not Found", "attribute not found", "")
		return
	}
	var req struct {
		TemplateIDs *[]string `json:"template_ids"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		problem(w, 400, "Bad Request", "invalid payload", "")
		return
	}
	if req.TemplateIDs != nil {
		for _, id := range *req.TemplateIDs {
			if s.tplByID(id) == nil {
				validation(w, map[string][]string{"template_ids": {"Unknown template " + id}})
				return
			}
		}
		// Replace semantics, as documented for PATCH /attributes/{id}.
		for _, t := range s.templates {
			t.AttributeIDs = without(t.AttributeIDs, a.ID)
		}
		for _, id := range *req.TemplateIDs {
			t := s.tplByID(id)
			t.AttributeIDs = appendUnique(t.AttributeIDs, a.ID)
		}
	}
	w.WriteHeader(204)
}

// --- helpers ---------------------------------------------------------------

func (s *Server) id() string {
	s.nextID++
	return fmt.Sprintf("01900000-0000-7000-8000-%012d", s.nextID)
}

func (s *Server) tplBySlug(slug string) *template {
	for _, t := range s.templates {
		if t.Slug == slug {
			return t
		}
	}
	return nil
}

func (s *Server) tplByID(id string) *template {
	for _, t := range s.templates {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (s *Server) attrBySlug(slug string) *attribute {
	for _, a := range s.attributes {
		if a.Slug == slug {
			return a
		}
	}
	return nil
}

func (s *Server) attrByID(id string) *attribute {
	for _, a := range s.attributes {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func typeToEnums(typ string) (attributeType, dataType int) {
	switch typ {
	case "metric":
		return 1, 1
	case "list":
		return 2, 0
	case "reference":
		return 3, 0
	}
	return 0, map[string]int{"string": 0, "number": 1, "boolean": 2, "datetime": 3, "date": 4, "file": 5, "image": 6, "markdown": 7}[typ]
}

func enumsToType(attributeType, dataType int) string {
	switch attributeType {
	case 1:
		return "metric"
	case 2:
		return "list"
	case 3:
		return "reference"
	}
	return []string{"string", "number", "boolean", "datetime", "date", "file", "image", "markdown"}[dataType]
}

func slugify(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func appendUnique(ids []string, id string) []string {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	return append(ids, id)
}

func without(ids []string, id string) []string {
	out := ids[:0:0]
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func problem(w http.ResponseWriter, status int, title, detail, code string) {
	m := map[string]any{"type": "error/" + strings.ToLower(strings.ReplaceAll(title, " ", "_")), "title": title, "status": status, "detail": detail}
	if code != "" {
		m["code"] = code
	}
	writeJSON(w, status, m)
}

func validation(w http.ResponseWriter, errs map[string][]string) {
	writeJSON(w, 422, map[string]any{"type": "error/validation", "title": "Validation Failed", "status": 422, "detail": "The given data was invalid.", "errors": errs})
}

// AddOptionLocked appends a list option to an attribute; for Before hooks.
func (s *Server) AddOptionLocked(attrSlug, value string) {
	a := s.attrBySlug(attrSlug)
	if a == nil {
		panic("omnitest.AddOptionLocked: unknown attribute " + attrSlug)
	}
	a.Options = append(a.Options, listItem{ID: s.id(), Value: value})
}
