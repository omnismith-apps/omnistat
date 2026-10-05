package omnitest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// EntitySnapshot is a read-only view of a stored entity.
type EntitySnapshot struct {
	ID, TemplateSlug string
	Values           map[string]any
	CreatedAt        string
	ExternalKey      string
}

// AddEntity seeds an entity on the given template (by slug) and returns its id.
// createdAt lets tests control FR-013 ordering; empty means now.
func (s *Server) AddEntity(templateSlug string, values map[string]any, createdAt string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addEntityLocked(templateSlug, values, createdAt)
}

// AddEntityLocked is AddEntity for use inside Before hooks.
func (s *Server) AddEntityLocked(templateSlug string, values map[string]any, createdAt string) string {
	return s.addEntityLocked(templateSlug, values, createdAt)
}

func (s *Server) addEntityLocked(templateSlug string, values map[string]any, createdAt string) string {
	t := s.tplBySlug(templateSlug)
	if t == nil {
		panic("omnitest.AddEntity: unknown template " + templateSlug)
	}
	if createdAt == "" {
		createdAt = time.Now().UTC().Add(time.Duration(len(s.entities)) * time.Millisecond).Format(time.RFC3339Nano)
	}
	e := &entity{ID: s.id(), TemplateID: t.ID, Values: values, CreatedAt: createdAt}
	s.entities = append(s.entities, e)
	return e.ID
}

// Entities returns all entities in creation order.
func (s *Server) Entities() []EntitySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []EntitySnapshot
	for _, e := range s.entities {
		snap := EntitySnapshot{ID: e.ID, Values: e.Values, CreatedAt: e.CreatedAt, ExternalKey: e.ExternalKey}
		if t := s.tplByID(e.TemplateID); t != nil {
			snap.TemplateSlug = t.Slug
		}
		out = append(out, snap)
	}
	return out
}

func (s *Server) searchEntities(w http.ResponseWriter, templateID string, body []byte) {
	t := s.tplByID(templateID)
	if t == nil {
		t = s.tplBySlug(templateID)
	}
	if t == nil {
		problem(w, 404, "Not Found", "template not found", "")
		return
	}
	var req struct {
		FilterGroups [][]struct {
			Field    string `json:"field"`
			Operator string `json:"operator"`
			Value    any    `json:"value"`
		} `json:"filter_groups"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		problem(w, 400, "Bad Request", "invalid payload", "")
		return
	}
	s.searches++
	var matches []*entity
	for _, e := range s.entities {
		if e.TemplateID != t.ID || e.hiddenUntil >= s.searches {
			continue
		}
		ok := len(req.FilterGroups) == 0
		for _, group := range req.FilterGroups { // groups are OR-ed, filters AND-ed
			all := true
			for _, f := range group {
				if f.Operator != "eq" || fmt.Sprint(e.Values[f.Field]) != fmt.Sprint(f.Value) {
					all = false
					break
				}
			}
			if all {
				ok = true
				break
			}
		}
		if ok {
			matches = append(matches, e)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].CreatedAt < matches[j].CreatedAt })
	data := []map[string]any{}
	for _, e := range matches {
		rec := map[string]any{
			"id": e.ID, "template_id": e.TemplateID, "template_slug": t.Slug,
			"created_at": e.CreatedAt, "updated_at": e.CreatedAt, "attribute_values": e.Values,
		}
		if e.ExternalKey != "" {
			rec["external_key"] = e.ExternalKey
		}
		data = append(data, rec)
	}
	writeJSON(w, 200, map[string]any{"data": data, "total": len(data), "limit": 50, "offset": 0})
}

func (s *Server) createEntity(w http.ResponseWriter, templateRef string, body []byte) {
	t := s.tplBySlug(templateRef)
	if t == nil {
		t = s.tplByID(templateRef)
	}
	if t == nil {
		problem(w, 404, "Not Found", "template not found", "")
		return
	}
	var req struct {
		Attributes  map[string]any `json:"attributes"`
		ExternalKey *string        `json:"external_key"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		problem(w, 400, "Bad Request", "invalid payload", "")
		return
	}
	if !s.attributesOnTemplate(w, t, req.Attributes) {
		return
	}
	if req.ExternalKey != nil && s.liveByKey(t.ID, *req.ExternalKey) != nil {
		problem(w, 409, "Conflict", "external_key is held by another live record", "")
		return
	}
	id := s.addEntityLocked(t.Slug, req.Attributes, "")
	e := s.entityByID(id)
	e.hiddenUntil = s.searches + s.SearchLag // SearchLag: not searchable yet
	if req.ExternalKey != nil {
		e.ExternalKey = *req.ExternalKey
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

// attributesOnTemplate answers 422 unless every attribute exists and belongs
// to the template.
func (s *Server) attributesOnTemplate(w http.ResponseWriter, t *template, attrs map[string]any) bool {
	for slug := range attrs {
		a := s.attrBySlug(slug)
		if a == nil {
			validation(w, map[string][]string{"attributes." + slug: {"Unknown attribute."}})
			return false
		}
		bound := false
		for _, id := range t.AttributeIDs {
			if id == a.ID {
				bound = true
			}
		}
		if !bound {
			validation(w, map[string][]string{"attributes." + slug: {"Attribute does not belong to the template."}})
			return false
		}
	}
	return true
}

// liveByKey is the live record of a template holding key: the platform's
// partial unique index (spec 011 FR-007).
func (s *Server) liveByKey(templateID, key string) *entity {
	for _, e := range s.entities {
		if e.TemplateID == templateID && e.ExternalKey == key && key != "" {
			return e
		}
	}
	return nil
}

func (s *Server) tplByRef(ref string) *template {
	if t := s.tplBySlug(ref); t != nil {
		return t
	}
	return s.tplByID(ref)
}

// entityByKey is GET /entities/template/{t}/by-key?key=: the live record
// holding the key, read like GET /entities/{id}; 404 when none does. Like
// the platform, it reads the record itself and does not lag.
func (s *Server) entityByKey(w http.ResponseWriter, templateRef string, q url.Values) {
	t := s.tplByRef(templateRef)
	if t == nil {
		problem(w, 404, "Not Found", "template not found", "")
		return
	}
	e := s.liveByKey(t.ID, q.Get("key"))
	if e == nil {
		problem(w, 404, "Not Found", "no live record holds this key", "")
		return
	}
	s.getEntity(w, e.ID, q)
}

// upsertByKey is PUT /entities/template/{t}/by-key: create the record with
// the key (201), or partially update the live record holding it (200).
func (s *Server) upsertByKey(w http.ResponseWriter, templateRef string, body []byte) {
	t := s.tplByRef(templateRef)
	if t == nil {
		problem(w, 404, "Not Found", "template not found", "")
		return
	}
	var req struct {
		ExternalKey *string        `json:"external_key"`
		Attributes  map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.ExternalKey == nil || req.Attributes == nil {
		problem(w, 400, "Bad Request", "external_key and attributes are required", "")
		return
	}
	key := strings.TrimSpace(*req.ExternalKey)
	if key == "" || len(key) > 255 {
		validation(w, map[string][]string{"external_key": {"1–255 characters after trimming."}})
		return
	}
	if s.UpsertRace > 0 {
		s.UpsertRace--
		problem(w, 409, "Conflict", "the key was taken by a concurrent write; send the request again", "")
		return
	}
	if !s.attributesOnTemplate(w, t, req.Attributes) {
		return
	}
	if e := s.liveByKey(t.ID, key); e != nil {
		for slug, v := range req.Attributes {
			e.Values[slug] = v
			e.History = append(e.History, Write{Slug: slug, Value: v})
		}
		writeJSON(w, 200, map[string]any{"id": e.ID, "created": false})
		return
	}
	values := map[string]any{}
	for slug, v := range req.Attributes {
		values[slug] = v
	}
	id := s.addEntityLocked(t.Slug, values, "")
	e := s.entityByID(id)
	e.ExternalKey = key
	e.hiddenUntil = s.searches + s.SearchLag // search lags; the key does not
	writeJSON(w, 201, map[string]any{"id": id, "created": true})
}

// DeleteEntity removes an entity, as a user deleting it in Omnismith would; a
// deleted record releases its key.
func (s *Server) DeleteEntity(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.entities {
		if e.ID == id {
			s.entities = append(s.entities[:i], s.entities[i+1:]...)
			return
		}
	}
}

// SetEntityKey seeds an external key on an existing entity.
func (s *Server) SetEntityKey(id, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entityByID(id); e != nil {
		e.ExternalKey = key
	}
}

// SetEntityKeyLocked is SetEntityKey for use inside Before hooks.
func (s *Server) SetEntityKeyLocked(id, key string) {
	if e := s.entityByID(id); e != nil {
		e.ExternalKey = key
	}
}

// EntityValues returns an entity's current dimension values (slug → value),
// or nil when it does not exist.
func (s *Server) EntityValues(id string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entityByID(id)
	if e == nil {
		return nil
	}
	out := make(map[string]any, len(e.Values))
	for k, v := range e.Values {
		out[k] = v
	}
	return out
}

// getEntity is GET /entities/{id}: the entity's dimension values, projected
// to `fields` when given (an array sent as `fields=a,b`, per the spec's
// `style: form, explode: false`). Values are strings, as the real
// API returns them. The default shape is a slug → value map; `verbose=true`
// returns the array of {id, slug, value, custom_value} objects instead — both
// verified against the local API (spec 005 T009).
func (s *Server) getEntity(w http.ResponseWriter, id string, q url.Values) {
	e := s.entityByID(id)
	if e == nil {
		problem(w, 404, "Not Found", "no entity "+id, "")
		return
	}
	want := map[string]bool{}
	if f := q.Get("fields"); f != "" {
		for _, slug := range strings.Split(f, ",") {
			want[slug] = true
		}
	}
	slugs := make([]string, 0, len(e.Values))
	for slug := range e.Values {
		if len(want) == 0 || want[slug] {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	var values any
	if q.Get("verbose") == "true" || s.VerboseEntities {
		arr := make([]map[string]any, 0, len(slugs))
		for _, slug := range slugs {
			v := fmt.Sprint(e.Values[slug])
			arr = append(arr, map[string]any{"id": slug + "-id", "slug": slug, "value": v, "custom_value": v, "reference_entity_id": nil})
		}
		values = arr
	} else {
		m := make(map[string]string, len(slugs))
		for _, slug := range slugs {
			m[slug] = fmt.Sprint(e.Values[slug])
		}
		values = m
	}
	writeJSON(w, 200, map[string]any{
		"id": e.ID, "template_id": e.TemplateID, "attribute_values": values, "external_key": nullable(e.ExternalKey),
		"list_item_ids": map[string]string{}, "reference_entity_ids": map[string]string{}, "file_ids": map[string]string{},
	})
}

// EntityHistory returns every dimension write an entity received, in order.
func (s *Server) EntityHistory(id string) []Write {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entityByID(id); e != nil {
		return append([]Write(nil), e.History...)
	}
	return nil
}

// EntityMetrics returns the observations ingested for an entity, per slug.
func (s *Server) EntityMetrics(id string) map[string][]Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entityByID(id)
	if e == nil {
		return nil
	}
	out := make(map[string][]Observation, len(e.Metrics))
	for k, v := range e.Metrics {
		out[k] = append([]Observation(nil), v...)
	}
	return out
}

func (s *Server) entityByID(id string) *entity {
	for _, e := range s.entities {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// updateEntity is PATCH /entities/{id}: partial update, scalar or backfill
// object per attribute; 404 unknown entity; 422 unknown attribute or bad
// updated_at; 422 metric attribute in a dimension write is allowed by the
// platform but not modelled here.
func (s *Server) updateEntity(w http.ResponseWriter, id string, body []byte) {
	e := s.entityByID(id)
	if e == nil {
		problem(w, 404, "Not Found", "entity not found", "")
		return
	}
	var req struct {
		Attributes  map[string]json.RawMessage `json:"attributes"`
		ExternalKey *string                    `json:"external_key"`
	}
	if err := json.Unmarshal(body, &req); err != nil || (len(req.Attributes) == 0 && req.ExternalKey == nil) {
		problem(w, 400, "Bad Request", "send attributes, external_key, or both", "")
		return
	}
	if req.ExternalKey != nil {
		if other := s.liveByKey(e.TemplateID, *req.ExternalKey); other != nil && other.ID != e.ID {
			problem(w, 409, "Conflict", "external_key is held by another live record", "")
			return
		}
		e.ExternalKey = *req.ExternalKey
	}
	errs := map[string][]string{}
	writes := map[string]Write{}
	for slug, raw := range req.Attributes {
		if s.attrBySlug(slug) == nil {
			errs["attributes."+slug] = []string{"Unknown attribute."}
			continue
		}
		var backfill struct {
			Value     any     `json:"value"`
			UpdatedAt *string `json:"updated_at"`
		}
		var value any
		var at string
		if json.Unmarshal(raw, &backfill) == nil && strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
			value = backfill.Value
			if backfill.UpdatedAt != nil {
				if _, err := time.Parse(time.RFC3339, *backfill.UpdatedAt); err != nil {
					errs["attributes."+slug] = []string{"updated_at must be RFC 3339."}
					continue
				}
				at = *backfill.UpdatedAt
			}
		} else if err := json.Unmarshal(raw, &value); err != nil {
			errs["attributes."+slug] = []string{"Invalid value."}
			continue
		}
		writes[slug] = Write{Slug: slug, Value: value, UpdatedAt: at}
	}
	if len(errs) > 0 {
		validation(w, errs)
		return
	}
	for slug, wr := range writes {
		e.Values[slug] = wr.Value
		e.History = append(e.History, wr)
	}
	writeJSON(w, 200, map[string]any{"id": e.ID, "template_id": e.TemplateID, "attribute_values": e.Values})
}

// ingestMetrics is POST /entities/{id}/metrics: records observations, 202.
func (s *Server) ingestMetrics(w http.ResponseWriter, id string, body []byte) {
	e := s.entityByID(id)
	if e == nil {
		problem(w, 404, "Not Found", "entity not found", "")
		return
	}
	var req struct {
		MetricValues []struct {
			AttributeSlug string  `json:"attribute_slug"`
			AttributeID   string  `json:"attribute_id"`
			Value         string  `json:"value"`
			UpdatedAt     *string `json:"updated_at"`
		} `json:"metric_values"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		problem(w, 400, "Bad Request", "invalid payload", "")
		return
	}
	errs := map[string][]string{}
	for i, mv := range req.MetricValues {
		slug := mv.AttributeSlug
		if slug == "" {
			if a := s.attrByID(mv.AttributeID); a != nil {
				slug = a.Slug
			}
		}
		a := s.attrBySlug(slug)
		switch {
		case a == nil:
			errs[fmt.Sprintf("metric_values.%d", i)] = []string{"Unknown attribute."}
		case a.AttributeType != 1:
			errs[fmt.Sprintf("metric_values.%d", i)] = []string{"Not a metric attribute."}
		}
	}
	if len(errs) > 0 {
		validation(w, errs)
		return
	}
	if e.Metrics == nil {
		e.Metrics = map[string][]Observation{}
	}
	for _, mv := range req.MetricValues {
		slug := mv.AttributeSlug
		if slug == "" {
			slug = s.attrByID(mv.AttributeID).Slug
		}
		at := ""
		if mv.UpdatedAt != nil {
			at = *mv.UpdatedAt
		}
		e.Metrics[slug] = append(e.Metrics[slug], Observation{Value: mv.Value, UpdatedAt: at})
	}
	w.WriteHeader(202)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
