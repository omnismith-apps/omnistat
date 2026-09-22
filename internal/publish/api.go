// Package publish sends buffered samples to the host entity (spec 003
// FR-012, FR-012a, FR-015…017, FR-021): dimensions as one partial entity
// update with backfilled timestamps, metrics as chunked ingestions.
package publish

import (
	"context"
	"errors"
	"time"
)

// Errors an API implementation must make recognisable with errors.Is /
// errors.As, so that this package never imports the SDK layer.
var (
	// ErrNotFound: the entity does not exist (HTTP 404).
	ErrNotFound = errors.New("entity not found")
	// ErrRejected: the platform refused the content (HTTP 422); the error
	// should also implement Rejected.
	ErrRejected = errors.New("rejected by the platform")
)

// Rejected exposes the platform's field errors of a rejected write, keyed
// like `attributes.<slug>`.
type Rejected interface {
	RejectedFields() map[string][]string
}

// API is the narrow, additive-only view of Omnismith that publishing needs
// (ADR-0003): a partial entity update and metric ingestion. No replace, no
// delete.
type API interface {
	// UpdateEntity writes the given attributes (slug → value observed at a
	// time) to the entity. Values are already rendered as the platform
	// expects (see Render).
	UpdateEntity(ctx context.Context, entityID string, attrs map[string]Backfill) error
	// IngestMetrics appends observations to the entity's metric series.
	IngestMetrics(ctx context.Context, entityID string, obs []Metric) error
}

// Backfill is a dimension value with the instant it was observed.
type Backfill struct {
	// Value is a string or a bool — numbers, dates and list item ids are
	// rendered as strings (see Render) to avoid float32 rounding in the
	// SDK's scalar union. omnistat never clears a value (FR-002 rejects nil).
	Value any
	At    time.Time
}

// Metric is one observation of a metric attribute.
type Metric struct {
	Slug  string
	Value string
	At    time.Time
}
