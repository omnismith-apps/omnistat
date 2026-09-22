package omni

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	omnismithsdk "github.com/omnismith-sdk/go"

	"github.com/omnismith-apps/omnistat/internal/identity"
)

var _ identity.API = (*Client)(nil)

// FindEntities implements identity.API: exact match on one attribute, oldest first.
func (c *Client) FindEntities(ctx context.Context, templateID, attrSlug, value string) ([]identity.EntitySummary, error) {
	filter := omnismithsdk.NewEntityFilter(attrSlug, "eq")
	filter.SetValue(omnismithsdk.EntityFilterValue{String: &value})
	req := omnismithsdk.NewSearchEntitiesRequest()
	req.SetFilterGroups([][]omnismithsdk.EntityFilter{{*filter}})
	req.SetFields([]string{attrSlug})
	res, resp, err := c.sdk.EntityAPI.SearchEntities(ctx, templateID).SearchEntitiesRequest(*req).
		SortField("created_at").SortDirection("asc").Limit(50).Execute() //nolint:bodyclose // Execute drains and closes the body
	if err != nil {
		return nil, mapErr("search entities", resp, err)
	}
	var out []identity.EntitySummary
	for _, e := range res.GetData() {
		out = append(out, identity.EntitySummary{ID: e.GetId(), CreatedAt: e.GetCreatedAt()})
	}
	return out, nil
}

// CreateEntity implements identity.API. Values are sent as strings, numbers
// or booleans according to their Go type.
func (c *Client) CreateEntity(ctx context.Context, templateSlug string, attrs map[string]any) (string, error) {
	values := make(map[string]omnismithsdk.EntityAttributesInputValue, len(attrs))
	for slug, v := range attrs {
		var in omnismithsdk.EntityAttributesInputValue
		switch x := v.(type) {
		case string:
			in.String = &x
		case bool:
			in.Bool = &x
		case float64:
			f := float32(x)
			in.Float32 = &f
		case float32:
			in.Float32 = &x
		case int:
			f := float32(x)
			in.Float32 = &f
		default:
			return "", fmt.Errorf("create entity: unsupported value type %T for %s", v, slug)
		}
		values[slug] = in
	}
	req := omnismithsdk.NewCreateEntityRequest(values)
	res, resp, err := c.sdk.EntityAPI.CreateEntity(ctx, templateSlug).CreateEntityRequest(*req).Execute() //nolint:bodyclose // Execute drains and closes the body
	if err != nil {
		return "", mapErr("create entity", resp, err)
	}
	return res.GetId(), nil
}

// ChartPoint is one bucket of a metric series.
type ChartPoint struct {
	At    time.Time
	Value float64
}

// EntityChart reads a metric back as a time series (spec 004 NFR-006). It is
// the only read of ingested metrics omnistat performs, and exists so that
// sandbox acceptance can prove the ingest path end to end rather than trusting
// the platform's 202.
//
// start and end are sent as Unix epoch seconds, which is what the endpoint
// wants; milliseconds are accepted and silently return an empty series.
// bucketWidth must be one of the platform's interval strings ("1 second",
// "1 minute", … ) — the default is "1 hour", which collapses a short run into
// a single point. See docs/reference/omnismith-api-notes.md.
func (c *Client) EntityChart(ctx context.Context, entityID string, attributeIDs []string, start, end time.Time, bucketWidth, aggregateFunc string) (map[string][]ChartPoint, error) {
	from, err := epochSeconds(start)
	if err != nil {
		return nil, fmt.Errorf("entity chart start: %w", err)
	}
	to, err := epochSeconds(end)
	if err != nil {
		return nil, fmt.Errorf("entity chart end: %w", err)
	}
	req := c.sdk.EntityAPI.GetEntityChart(ctx, entityID).
		AttributeIds(strings.Join(attributeIDs, ",")).
		Start(from).
		End(to)
	if bucketWidth != "" {
		req = req.BucketWidth(bucketWidth)
	}
	if aggregateFunc != "" {
		req = req.AggregateFunc(aggregateFunc)
	}
	res, resp, err := req.Execute() //nolint:bodyclose // Execute drains and closes the body
	if err != nil {
		return nil, mapErr("entity chart", resp, err)
	}
	out := map[string][]ChartPoint{}
	for _, s := range res.GetSeries() {
		id := s.GetAttributeId()
		for _, p := range s.GetData() {
			out[id] = append(out[id], ChartPoint{At: p.GetTime(), Value: float64(p.GetValue())})
		}
	}
	return out, nil
}

// epochSeconds renders a time the way the chart endpoint wants it. The SDK
// types the parameter as int32, so the endpoint cannot address a time beyond
// January 2038; rather than wrap silently, a time it cannot express is an
// error. (Milliseconds are not an alternative: the endpoint accepts them and
// returns an empty series.)
func epochSeconds(t time.Time) (int32, error) {
	sec := t.Unix()
	if sec < math.MinInt32 || sec > math.MaxInt32 {
		return 0, fmt.Errorf("%s is outside the range the API can express (32-bit epoch seconds)", t.UTC().Format(time.RFC3339))
	}
	return int32(sec), nil
}
