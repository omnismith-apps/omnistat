package omnitest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// EntitySnapshot is a read-only view of a stored entity.
type EntitySnapshot struct {
	ID, TemplateSlug string
	Values           map[string]any
	CreatedAt        string
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
		snap := EntitySnapshot{ID: e.ID, Values: e.Values, CreatedAt: e.CreatedAt}
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
	var matches []*entity
	for _, e := range s.entities {
		if e.TemplateID != t.ID {
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
		data = append(data, map[string]any{
			"id": e.ID, "template_id": e.TemplateID, "template_slug": t.Slug,
			"created_at": e.CreatedAt, "updated_at": e.CreatedAt, "attribute_values": e.Values,
		})
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
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		problem(w, 400, "Bad Request", "invalid payload", "")
		return
	}
	for slug := range req.Attributes {
		a := s.attrBySlug(slug)
		if a == nil {
			validation(w, map[string][]string{"attributes." + slug: {"Unknown attribute."}})
			return
		}
		bound := false
		for _, id := range t.AttributeIDs {
			if id == a.ID {
				bound = true
			}
		}
		if !bound {
			validation(w, map[string][]string{"attributes." + slug: {"Attribute does not belong to the template."}})
			return
		}
	}
	id := s.addEntityLocked(t.Slug, req.Attributes, "")
	writeJSON(w, 201, map[string]any{"id": id})
}
