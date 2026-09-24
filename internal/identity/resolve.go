package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/omnismith-apps/omnistat/internal/settle"
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

// Resolve finds or creates the host entity for value (FR-010…013), waiting
// for its own create to become searchable with the default settle policy.
// With dryRun it only reports what it would do (FR-017).
func Resolve(ctx context.Context, api API, t Target, value string, dryRun bool, log *slog.Logger) (Host, error) {
	return ResolveWith(ctx, api, t, value, dryRun, log, settle.Default())
}

// ResolveWith is Resolve with an explicit settle policy; tests pass one that
// never sleeps.
//
// Omnismith processes writes asynchronously, so an entity is not searchable
// for a moment after its create returns. The re-search of FR-012 therefore
// waits, within the policy's budget, until the search shows the entity this
// run created: only then does it also show any entity a concurrent first run
// created before it, and only then will the next resolution find ours rather
// than create another. If the budget runs out, the run keeps the entity it
// created (or the oldest one it can see) and warns; FR-013 settles any
// duplicate on a later run.
func ResolveWith(ctx context.Context, api API, t Target, value string, dryRun bool, log *slog.Logger, p settle.Policy) (Host, error) {
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
		// Re-search once our entity is visible: a concurrent first run may
		// have created one too (FR-012).
		search := func(ctx context.Context) ([]EntitySummary, error) {
			return api.FindEntities(ctx, t.TemplateID, t.AttributeSlug, value)
		}
		var settled bool
		found, settled, err = settle.Until(ctx, p, search, func(es []EntitySummary) bool { return contains(es, id) })
		if err != nil {
			return h, fmt.Errorf("identity: re-search host entity: %w", err)
		}
		if !settled {
			log.Warn("host entity not yet searchable; concurrent-creation check inconclusive", "entity", id)
			if len(found) == 0 {
				found = []EntitySummary{{ID: id}}
			}
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

func contains(es []EntitySummary, id string) bool {
	for _, e := range es {
		if e.ID == id {
			return true
		}
	}
	return false
}
