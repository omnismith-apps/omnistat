package omni

import (
	"context"
	"fmt"
	"time"

	omnismithsdk "github.com/omnismith-sdk/go"

	"github.com/omnismith-apps/omnistat/internal/publish"
)

var _ publish.API = (*Client)(nil)

// stamp renders an observation time the way the platform accepts it: UTC,
// at most microsecond precision (RFC 3339 with nanoseconds is rejected with
// 422 — found in the 003 sandbox run).
func stamp(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// UpdateEntity implements publish.API (spec 003 FR-012): a partial update
// whose every attribute is a backfill object `{value, updated_at}`.
func (c *Client) UpdateEntity(ctx context.Context, entityID string, attrs map[string]publish.Backfill) error {
	values := make(map[string]omnismithsdk.EntityAttributesInputValue, len(attrs))
	for slug, b := range attrs {
		var v omnismithsdk.EntityAttributesInputValueAnyOfValue
		switch x := b.Value.(type) {
		case string:
			v.String = &x
		case bool:
			v.Bool = &x
		default:
			return fmt.Errorf("update entity: unsupported value type %T for %s", b.Value, slug)
		}
		bf := omnismithsdk.NewEntityAttributesInputValueAnyOf(v)
		at := stamp(b.At)
		bf.UpdatedAt = &at
		values[slug] = omnismithsdk.EntityAttributesInputValue{EntityAttributesInputValueAnyOf: bf}
	}
	req := omnismithsdk.NewUpdateEntityRequest(values)
	resp, err := c.sdk.EntityAPI.UpdateEntity(ctx, entityID).UpdateEntityRequest(*req).Execute() //nolint:bodyclose // Execute drains and closes the body
	return mapErr("update entity", resp, err)
}

// IngestMetrics implements publish.API (spec 003 FR-012/017): observations
// by slug, values as decimal strings, explicit timestamps.
func (c *Client) IngestMetrics(ctx context.Context, entityID string, obs []publish.Metric) error {
	values := make([]omnismithsdk.IngestMetricsRequestMetricValuesInner, 0, len(obs))
	for _, o := range obs {
		mv := omnismithsdk.NewIngestMetricsRequestMetricValuesInner()
		mv.SetAttributeSlug(o.Slug)
		mv.SetValue(o.Value)
		mv.SetUpdatedAt(stamp(o.At))
		values = append(values, *mv)
	}
	req := omnismithsdk.NewIngestMetricsRequest()
	req.SetMetricValues(values)
	resp, err := c.sdk.EntityAPI.IngestEntityMetrics(ctx, entityID).IngestMetricsRequest(*req).Execute() //nolint:bodyclose // Execute drains and closes the body
	return mapErr("ingest metrics", resp, err)
}
