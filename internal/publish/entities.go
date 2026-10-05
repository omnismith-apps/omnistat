package publish

import (
	"context"
	"errors"
	"log/slog"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/identity"
)

// Resolver finds the module-owned entity holding a key (spec 011 FR-011),
// remembering it until Forget. identity.Keyed implements it.
type Resolver interface {
	Resolve(ctx context.Context, module, template, key string) (string, identity.Outcome, error)
	Forget(template, key string)
}

// Entities publishes the host entity and every module-owned entity with
// something buffered (spec 011 FR-014…FR-019): the host first, then each
// entity in order, each on its own, so that one entity's failure costs only
// that entity's publish.
type Entities struct {
	// Host publishes to the host entity. Its API, list items, printer and
	// pending options are shared with every entity's publisher.
	Host     *Publisher
	Resolver Resolver
	// HostLink maps an entity template's resolved slug to its host link
	// attribute's slug.
	HostLink map[string]string
	Log      *slog.Logger
}

// Publish sends everything buffered. The error is the host's: a host entity
// that no longer exists (ErrEntityGone) ends the run (003 FR-015). Failures of
// module entities are logged and counted in Result.EntitiesFailed, and their
// samples stay buffered (FR-014, FR-015).
func (e *Entities) Publish(ctx context.Context, bufs *collect.Buffers) (Result, error) {
	log := e.Log
	if log == nil {
		log = slog.Default()
	}
	var total Result
	total.Ack = collect.Ack{Metrics: map[string]int{}}

	var hostErr error
	if batch := bufs.Host().Snapshot(); !batch.Empty() {
		res, err := e.Host.Publish(ctx, batch)
		bufs.Host().Ack(batch, res.Ack)
		total.add(res)
		if errors.Is(err, ErrEntityGone) {
			return total, err
		}
		hostErr = err
	}

	for _, t := range bufs.Targets() {
		buf := bufs.For(t)
		batch := buf.Snapshot()
		if batch.Empty() {
			continue
		}
		id, outcome, err := e.Resolver.Resolve(ctx, t.Module, t.Template, t.Key)
		if err != nil {
			total.EntitiesFailed++
			log.Error("entity not resolved; keeping its values", "module", t.Module, "template", t.Template, "key", t.Key, "error", err)
			continue
		}
		p := *e.Host
		p.EntityID = id
		p.Entity = &EntityLabel{Module: t.Module, Template: t.Template, Key: t.Key, WouldCreate: outcome == identity.WouldCreate}
		if slug := e.HostLink[t.Template]; slug != "" {
			p.HostLink = &HostLink{Module: t.Module, Slug: slug, EntityID: e.Host.EntityID}
		}
		res, err := p.Publish(ctx, batch)
		buf.Ack(batch, res.Ack)
		total.add(res)
		switch {
		case err == nil:
			total.Entities++
		case errors.Is(err, ErrEntityGone):
			// Deleted while we ran: create it again next time (FR-015).
			e.Resolver.Forget(t.Template, t.Key)
			total.EntitiesFailed++
			log.Warn("entity no longer exists; it will be created again at the next publish", "module", t.Module, "template", t.Template, "key", t.Key, "entity", id)
		default:
			total.EntitiesFailed++
			log.Error("entity publish failed; keeping its values", "module", t.Module, "template", t.Template, "key", t.Key, "entity", id, "error", err)
		}
	}
	return total, hostErr
}

func (r *Result) add(o Result) {
	r.Dimensions += o.Dimensions
	r.Observations += o.Observations
	r.Requests += o.Requests
	r.Dropped += o.Dropped
}
