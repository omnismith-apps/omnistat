// Package identity resolves the entities omnistat owns by their platform
// external key (spec 011 FR-007…FR-015): the host entity at startup, with a
// one-time adoption of hosts created before keys existed (spec 002, amended
// by 011 FR-012), and module-owned entities when they are first published.
package identity

import (
	"context"
	"errors"
	"time"
)

// ErrKeyTaken is what an API returns (wrapped) when the platform refuses a
// key because another live record of the template holds it, or a concurrent
// write took it first (HTTP 409).
var ErrKeyTaken = errors.New("external key is held by another record")

// API is the narrow, additive-only view of Omnismith that resolution needs
// (ADR-0003). It can set a key on a record that has none, but has no way to
// clear or change a key, and no update, delete or replace (spec 011 NFR-003).
type API interface {
	// FindEntities returns the entities of templateID whose attribute
	// attrSlug equals value, oldest first. Only adoption uses it.
	FindEntities(ctx context.Context, templateID, attrSlug, value string) ([]EntitySummary, error)
	// EntityByKey returns the id of the live record of the template holding
	// key, and whether one does.
	EntityByKey(ctx context.Context, templateSlug, key string) (id string, found bool, err error)
	// UpsertByKey creates the record of the template holding key, with the
	// given attribute values, or partially updates the one that holds it.
	UpsertByKey(ctx context.Context, templateSlug, key string, attrs map[string]any) (id string, created bool, err error)
	// SetEntityKey gives an existing record a key. Callers only ever use it
	// on a record that holds none (spec 011 FR-012).
	SetEntityKey(ctx context.Context, entityID, key string) error
}

// EntitySummary is what adoption needs to know about a candidate.
type EntitySummary struct {
	ID        string
	CreatedAt time.Time
	// Key is the record's external key, "" when it holds none.
	Key string
}
