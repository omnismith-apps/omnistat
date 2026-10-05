package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"unicode"
)

// MaxKeyLen bounds a key (spec 011 FR-008), well inside the platform's 255.
const MaxKeyLen = 128

// upsertAttempts bounds the retries of an upsert the platform answered with a
// conflict before resolution falls back to a lookup (spec 011 FR-011).
const upsertAttempts = 3

// ValidKey reports why key cannot identify an entity, or nil (spec 011
// FR-008): it must be trimmed, non-empty, at most MaxKeyLen characters and
// free of control characters.
func ValidKey(key string) error {
	switch {
	case key == "":
		return errors.New("empty key")
	case strings.TrimSpace(key) != key:
		return errors.New("key has leading or trailing space")
	case len([]rune(key)) > MaxKeyLen:
		return fmt.Errorf("key is longer than %d characters", MaxKeyLen)
	case strings.IndexFunc(key, unicode.IsControl) >= 0:
		return errors.New("key contains a control character")
	}
	return nil
}

// Outcome says how an entity was resolved (spec 011 FR-022).
type Outcome string

const (
	Found           Outcome = "found"
	Adopted         Outcome = "adopted"
	AdoptedOtherKey Outcome = "adopted-other-key"
	Created         Outcome = "created"
	WouldAdopt      Outcome = "would-adopt"
	WouldCreate     Outcome = "would-create"
)

// Target says where the host identity lives: the host template and the
// (possibly remapped) identity attribute slug, resolved by feature 001.
type Target struct {
	TemplateSlug  string
	TemplateID    string
	AttributeSlug string
}

// Host is the outcome of host resolution. It is held in memory for the run
// and never persisted (002 FR-015).
type Host struct {
	// EntityID is empty only when Outcome is WouldCreate.
	EntityID string
	Identity string
	Outcome  Outcome
	// Created is true when this run created the entity.
	Created bool
	// WouldCreate is true in dry-run when no entity exists yet.
	WouldCreate bool
	// Duplicates lists every legacy entity carrying the identity when more
	// than one does (oldest first; EntityID is the first).
	Duplicates []string
	// OtherKey is the key an adopted entity already held (AdoptedOtherKey).
	OtherKey string
}

// ResolveHost finds the host entity by its external key, adopting an entity
// created before keys existed, or creates it (spec 011 FR-011, FR-012;
// amends 002 FR-010…FR-013). With dryRun it writes nothing and reports what
// it would do (002 FR-017, 011 FR-020).
//
// Neither the lookup nor the upsert lags behind the platform's writes (the
// key lives on the entity row under a unique index), so no settle wait is
// needed. Only adoption searches, and it searches for records an earlier
// version created long ago.
func ResolveHost(ctx context.Context, api API, t Target, key string, dryRun bool, log *slog.Logger) (Host, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := ValidKey(key); err != nil {
		return Host{}, fmt.Errorf("identity: %w", err)
	}
	if t.TemplateID == "" || t.TemplateSlug == "" || t.AttributeSlug == "" {
		return Host{}, errors.New("identity: target template/attribute not resolved (run schema reconciliation first)")
	}
	h := Host{Identity: key}
	done := func(h Host) (Host, error) {
		log.Info("host entity resolved", "template", t.TemplateSlug, "key", key, "entity", h.EntityID, "outcome", string(h.Outcome))
		return h, nil
	}

	// 1. The host already holds its key.
	id, found, err := api.EntityByKey(ctx, t.TemplateSlug, key)
	if err != nil {
		return h, fmt.Errorf("identity: look up host entity by key: %w", err)
	}
	if found {
		h.EntityID, h.Outcome = id, Found
		return done(h)
	}

	// 2. Adoption: a host entity an earlier version created, found by the
	// identity attribute (FR-012).
	legacy, err := api.FindEntities(ctx, t.TemplateID, t.AttributeSlug, key)
	if err != nil {
		return h, fmt.Errorf("identity: search host entity: %w", err)
	}
	if len(legacy) > 0 {
		sort.SliceStable(legacy, func(i, j int) bool { return legacy[i].CreatedAt.Before(legacy[j].CreatedAt) })
		pick := legacy[0]
		h.EntityID = pick.ID
		if len(legacy) > 1 {
			for _, e := range legacy {
				h.Duplicates = append(h.Duplicates, e.ID)
			}
			log.Warn("several host entities carry this identity; adopting the oldest", "entity", pick.ID, "duplicates", h.Duplicates)
		}
		switch {
		case pick.Key == key:
			// Keyed between our lookup and the search (a concurrent start).
			h.Outcome = Found
			return done(h)
		case pick.Key != "":
			// Never overwrite a key someone else set (NFR-003).
			h.Outcome, h.OtherKey = AdoptedOtherKey, pick.Key
			log.Warn("host entity holds another external key; using it and leaving the key unchanged", "entity", pick.ID, "key", pick.Key, "identity", key)
			return done(h)
		case dryRun:
			h.Outcome = WouldAdopt
			return h, nil
		}
		err := api.SetEntityKey(ctx, pick.ID, key)
		switch {
		case err == nil:
			h.Outcome = Adopted
			return done(h)
		case errors.Is(err, ErrKeyTaken):
			// Another record took the key meanwhile: that one is the host now.
			id, found, lerr := api.EntityByKey(ctx, t.TemplateSlug, key)
			if lerr != nil {
				return h, fmt.Errorf("identity: look up host entity by key: %w", lerr)
			}
			if !found {
				return h, fmt.Errorf("identity: adopt host entity %s: %w", pick.ID, err)
			}
			h.EntityID, h.Outcome, h.Duplicates = id, Found, nil
			return done(h)
		default:
			return h, fmt.Errorf("identity: adopt host entity %s: %w", pick.ID, err)
		}
	}

	// 3. A fresh host: create it holding its key, with the identity attribute.
	if dryRun {
		h.Outcome, h.WouldCreate = WouldCreate, true
		return h, nil
	}
	id, created, err := upsert(ctx, api, t.TemplateSlug, key, map[string]any{t.AttributeSlug: key})
	if err != nil {
		return h, fmt.Errorf("identity: create host entity: %w", err)
	}
	h.EntityID, h.Created = id, created
	h.Outcome = Found
	if created {
		h.Outcome = Created
	}
	return done(h)
}

// upsert creates or finds the record holding key, retrying a conflict
// boundedly and then settling it with a lookup (spec 011 FR-011).
func upsert(ctx context.Context, api API, template, key string, attrs map[string]any) (string, bool, error) {
	var last error
	for range upsertAttempts {
		id, created, err := api.UpsertByKey(ctx, template, key, attrs)
		if err == nil {
			return id, created, nil
		}
		if !errors.Is(err, ErrKeyTaken) {
			return "", false, err
		}
		last = err
	}
	id, found, err := api.EntityByKey(ctx, template, key)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, last
	}
	return id, false, nil
}
