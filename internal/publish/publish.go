package publish

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// ChunkSize is the maximum number of observations per ingestion request (FR-012).
const ChunkSize = 1000

// ErrEntityGone is returned when the host entity no longer exists: the run
// must end so that the next one recreates it (FR-015, US-3/3).
var ErrEntityGone = errors.New("host entity no longer exists")

// Publisher sends batches to one host entity.
type Publisher struct {
	API      API
	EntityID string
	// ListItems maps attribute slug → option value → list item id (FR-012a).
	ListItems map[string]map[string]string
	// PendingOptions (dry-run only, with Printer) are list options the schema
	// plan would create: slug → option value. Their values are printed as
	// publishable rather than dropped (FR-021). Never used for a real publish.
	PendingOptions map[string]map[string]bool
	Log            *slog.Logger
	// Printer, when set, receives what would be sent instead of the API
	// (FR-021 dry-run). API may then be nil.
	Printer *Printer
}

// Result says what a publish achieved. Ack is what the buffer may forget.
type Result struct {
	Ack          collect.Ack
	Dimensions   int
	Observations int
	Requests     int
	Dropped      int
}

// Publish sends a batch: at most one dimension update, then metric chunks
// (FR-012). It returns the partial Result with the error when a write fails
// so the caller can ack what was accepted and keep the rest (FR-009).
func (p *Publisher) Publish(ctx context.Context, b collect.Batch) (Result, error) {
	log := p.Log
	if log == nil {
		log = slog.Default()
	}
	res := Result{Ack: collect.Ack{Metrics: map[string]int{}}}
	if b.Empty() {
		return res, nil
	}

	// Dimensions: render, dropping what cannot be rendered (FR-002/012a).
	dims := map[string]Backfill{}
	var dimLines []Line
	for _, s := range b.Dims {
		if label, ok := s.Value.(string); ok && p.Printer != nil && s.Kind == manifest.KindList && p.PendingOptions[s.Slug][label] {
			dims[s.Slug] = Backfill{Value: label, At: s.At}
			dimLines = append(dimLines, Line{Module: s.Module, Key: s.Key, Slug: s.Slug, Value: label, At: s.At, PendingOption: true})
			continue
		}
		v, err := Render(s, p.ListItems)
		if err != nil {
			log.Error("observation dropped", "module", s.Module, "key", s.Key, "slug", s.Slug, "error", err)
			res.Ack.Dims = append(res.Ack.Dims, s.Slug)
			res.Dropped++
			continue
		}
		dims[s.Slug] = Backfill{Value: v, At: s.At}
		dimLines = append(dimLines, Line{Module: s.Module, Key: s.Key, Slug: s.Slug, Value: v, At: s.At})
	}
	// Metrics: render all, then chunk.
	var metrics []Metric
	var metricLines []Line
	for _, s := range b.Metrics {
		v, err := Render(s, p.ListItems)
		if err != nil {
			log.Error("observation dropped", "module", s.Module, "key", s.Key, "slug", s.Slug, "error", err)
			res.Ack.Metrics[s.Slug]++
			res.Dropped++
			continue
		}
		metrics = append(metrics, Metric{Slug: s.Slug, Value: v.(string), At: s.At})
		metricLines = append(metricLines, Line{Module: s.Module, Key: s.Key, Slug: s.Slug, Value: v, At: s.At})
	}

	if p.Printer != nil {
		p.Printer.Print(p.EntityID, dimLines, metricLines)
		for slug := range dims {
			res.Ack.Dims = append(res.Ack.Dims, slug)
		}
		for _, m := range metrics {
			res.Ack.Metrics[m.Slug]++
		}
		res.Dimensions, res.Observations = len(dims), len(metrics)
		return res, nil
	}

	if len(dims) > 0 {
		sent, err := p.updateDims(ctx, dims, log)
		res.Requests += sent.requests
		res.Ack.Dims = append(res.Ack.Dims, sent.acked...)
		res.Dropped += sent.dropped
		if err != nil {
			return res, err
		}
		res.Dimensions = len(sent.acked) - sent.dropped
	}
	for i := 0; i < len(metrics); i += ChunkSize {
		chunk := metrics[i:min(i+ChunkSize, len(metrics))]
		res.Requests++
		err := p.API.IngestMetrics(ctx, p.EntityID, chunk)
		switch {
		case err == nil:
			res.Observations += len(chunk)
		case errors.Is(err, ErrNotFound):
			return res, fmt.Errorf("%w (%s): %w", ErrEntityGone, p.EntityID, err)
		case errors.Is(err, ErrRejected):
			// The platform refuses something in this chunk; keeping it would
			// refuse forever. Drop it and go on (FR-015).
			log.Error("metric chunk rejected and dropped", "observations", len(chunk), "error", err)
			res.Dropped += len(chunk)
		default:
			return res, fmt.Errorf("ingest metrics: %w", err)
		}
		for _, m := range chunk {
			res.Ack.Metrics[m.Slug]++
		}
	}
	return res, nil
}

type dimResult struct {
	acked    []string
	dropped  int
	requests int
}

// updateDims performs the dimension update, retrying once without the
// attributes a 422 names (FR-015).
func (p *Publisher) updateDims(ctx context.Context, dims map[string]Backfill, log *slog.Logger) (dimResult, error) {
	var r dimResult
	for attempt := 0; attempt < 2 && len(dims) > 0; attempt++ {
		r.requests++
		err := p.API.UpdateEntity(ctx, p.EntityID, dims)
		if err == nil {
			for slug := range dims {
				r.acked = append(r.acked, slug)
			}
			sort.Strings(r.acked)
			return r, nil
		}
		if errors.Is(err, ErrNotFound) {
			return r, fmt.Errorf("%w (%s): %w", ErrEntityGone, p.EntityID, err)
		}
		var rej Rejected
		if !errors.Is(err, ErrRejected) || !errors.As(err, &rej) {
			return r, fmt.Errorf("update entity: %w", err)
		}
		fields := rej.RejectedFields()
		bad := rejectedSlugs(fields, dims)
		if len(bad) == 0 {
			// 422 without a recognisable field: nothing to remove, do not loop.
			return r, fmt.Errorf("update entity: %w", err)
		}
		for _, slug := range bad {
			log.Error("dimension rejected and dropped", "slug", slug, "errors", fields["attributes."+slug])
			delete(dims, slug)
			r.acked = append(r.acked, slug)
			r.dropped++
		}
	}
	return r, nil
}

// rejectedSlugs extracts the attributes a 422 names (`attributes.<slug>`),
// limited to the ones we sent.
func rejectedSlugs(fields map[string][]string, dims map[string]Backfill) []string {
	var out []string
	for field := range fields {
		slug, ok := strings.CutPrefix(field, "attributes.")
		if !ok {
			continue
		}
		if _, sent := dims[slug]; sent {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out
}

// Render turns a validated sample into what the platform expects (FR-012):
// strings for everything except booleans, list options as item ids
// (FR-012a). Numbers are formatted with the shortest exact representation.
func Render(s collect.Sample, listItems map[string]map[string]string) (any, error) {
	bad := func() (any, error) {
		return nil, fmt.Errorf("%s value has type %T, not the canonical type of %s (bug)", s.Slug, s.Value, s.Kind)
	}
	switch s.Kind {
	case manifest.KindText:
		if v, ok := s.Value.(string); ok {
			return v, nil
		}
	case manifest.KindNumber, manifest.KindMetric:
		if v, ok := s.Value.(float64); ok {
			return strconv.FormatFloat(v, 'f', -1, 64), nil
		}
	case manifest.KindBoolean:
		if v, ok := s.Value.(bool); ok {
			return v, nil
		}
	case manifest.KindDate:
		if v, ok := s.Value.(time.Time); ok {
			return v.UTC().Format("2006-01-02"), nil
		}
	case manifest.KindDatetime:
		if v, ok := s.Value.(time.Time); ok {
			return v.UTC().Format(time.RFC3339), nil
		}
	case manifest.KindList:
		v, ok := s.Value.(string)
		if !ok {
			return bad()
		}
		id, ok := listItems[s.Slug][v]
		if !ok {
			return nil, fmt.Errorf("list option %q of %s has no item in the project schema", v, s.Slug)
		}
		return id, nil
	default:
		return nil, fmt.Errorf("unknown kind %q", s.Kind)
	}
	return bad()
}
