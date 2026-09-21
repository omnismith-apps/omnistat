// Package identity resolves the host entity for this machine's identity
// (spec 002 FR-010…016): find it by the identity attribute, create it once
// if absent, never create a second one.
package identity

import (
	"context"
	"time"
)

// API is the narrow, additive-only view of Omnismith that resolution needs
// (ADR-0003): no update, delete or replace.
type API interface {
	// FindEntities returns the entities of templateID whose attribute
	// attrSlug equals value, oldest first.
	FindEntities(ctx context.Context, templateID, attrSlug, value string) ([]EntitySummary, error)
	// CreateEntity creates an entity on templateSlug with the given
	// attribute values (slug → value) and returns its id.
	CreateEntity(ctx context.Context, templateSlug string, attrs map[string]any) (string, error)
}

// EntitySummary is what resolution needs to know about a candidate.
type EntitySummary struct {
	ID        string
	CreatedAt time.Time
}
