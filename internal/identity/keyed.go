package identity

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Keyed resolves module-owned entities by key when they are first published
// (spec 011 FR-010, FR-011, FR-013…FR-015) and remembers their ids for the
// life of the process. It is safe for concurrent use.
type Keyed struct {
	API API
	// HostID is the resolved host entity, written as every new entity's host
	// link (FR-011).
	HostID string
	// HostLink maps an entity template's resolved slug to its host link
	// attribute's slug.
	HostLink map[string]string
	// DryRun looks up but never creates (FR-020).
	DryRun bool
	Log    *slog.Logger

	mu  sync.Mutex
	ids map[string]string // template + "\x00" + key → entity id
}

func cacheKey(template, key string) string { return template + "\x00" + key }

// Resolve returns the id of the entity of template holding key, creating it
// with only its host link set when none does. In dry-run an entity that does
// not exist yet yields an empty id and WouldCreate.
func (k *Keyed) Resolve(ctx context.Context, module, template, key string) (string, Outcome, error) {
	log := k.Log
	if log == nil {
		log = slog.Default()
	}
	k.mu.Lock()
	if id, ok := k.ids[cacheKey(template, key)]; ok {
		k.mu.Unlock()
		return id, Found, nil
	}
	k.mu.Unlock()

	id, found, err := k.API.EntityByKey(ctx, template, key)
	if err != nil {
		return "", "", fmt.Errorf("look up %s entity %q: %w", template, key, err)
	}
	outcome := Found
	if !found {
		if k.DryRun {
			return "", WouldCreate, nil
		}
		attrs := map[string]any{}
		if link := k.HostLink[template]; link != "" && k.HostID != "" {
			attrs[link] = k.HostID
		}
		var created bool
		id, created, err = upsert(ctx, k.API, template, key, attrs)
		if err != nil {
			return "", "", fmt.Errorf("create %s entity %q: %w", template, key, err)
		}
		if created {
			outcome = Created
		}
	}
	k.mu.Lock()
	if k.ids == nil {
		k.ids = map[string]string{}
	}
	k.ids[cacheKey(template, key)] = id
	k.mu.Unlock()
	log.Info("entity resolved", "module", module, "template", template, "key", key, "entity", id, "outcome", string(outcome))
	return id, outcome, nil
}

// Forget drops a remembered id, so that the next Resolve looks the entity up
// again (FR-015: it was deleted while the daemon ran).
func (k *Keyed) Forget(template, key string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.ids, cacheKey(template, key))
}
