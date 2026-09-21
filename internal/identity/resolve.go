package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
)

// Target says where the identity lives: the host template and the (possibly
// remapped) identity attribute slug, resolved by feature 001.
type Target struct {
	TemplateSlug  string
	TemplateID    string
	AttributeSlug string
}

// Host is the outcome of resolution. It is held in memory for the run and
// never persisted (FR-015).
type Host struct {
	EntityID string
	Identity string
	// Created is true when this run created the entity.
	Created bool
	// WouldCreate is true in dry-run when no entity exists yet.
	WouldCreate bool
	// Duplicates lists every entity carrying the identity when more than one
	// does (oldest first; EntityID is the first).
	Duplicates []string
}

// Resolve finds or creates the host entity for value (FR-010…013). With
// dryRun it only reports what it would do (FR-017).
func Resolve(ctx context.Context, api API, t Target, value string, dryRun bool, log *slog.Logger) (Host, error) {
	if log == nil {
		log = slog.Default()
	}
	if value == "" {
		return Host{}, errors.New("identity: empty identity value")
	}
	if t.TemplateID == "" || t.TemplateSlug == "" || t.AttributeSlug == "" {
		return Host{}, errors.New("identity: target template/attribute not resolved (run schema reconciliation first)")
	}
	h := Host{Identity: value}

	found, err := api.FindEntities(ctx, t.TemplateID, t.AttributeSlug, value)
	if err != nil {
		return h, fmt.Errorf("identity: search host entity: %w", err)
	}
	if len(found) == 0 {
		if dryRun {
			h.WouldCreate = true
			return h, nil
		}
		id, err := api.CreateEntity(ctx, t.TemplateSlug, map[string]any{t.AttributeSlug: value})
		if err != nil {
			return h, fmt.Errorf("identity: create host entity: %w", err)
		}
		h.Created = true
		log.Info("host entity created", "entity", id, "template", t.TemplateSlug)
		// Re-search: a concurrent first run may have created one too (FR-012).
		found, err = api.FindEntities(ctx, t.TemplateID, t.AttributeSlug, value)
		if err != nil {
			return h, fmt.Errorf("identity: re-search host entity: %w", err)
		}
		if len(found) == 0 {
			found = []EntitySummary{{ID: id}}
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].CreatedAt.Before(found[j].CreatedAt) })
	h.EntityID = found[0].ID
	if len(found) > 1 {
		for _, e := range found {
			h.Duplicates = append(h.Duplicates, e.ID)
		}
		log.Warn("several host entities carry this identity; using the oldest", "entity", h.EntityID, "duplicates", h.Duplicates)
	}
	log.Debug("host entity resolved", "entity", h.EntityID, "created", h.Created)
	return h, nil
}
