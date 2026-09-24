package omni

import (
	"context"
	"fmt"
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
	from := epochSeconds(start)
	to := epochSeconds(end)
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

// epochSeconds renders a time the way the chart endpoint wants it.
// (Milliseconds are not an alternative: the endpoint accepts them and
// returns an empty series.)
func epochSeconds(t time.Time) int64 {
	return t.Unix()
}

// EntityValues reads an entity's current dimension values (slug → value, as
// the strings the API returns), projected to slugs when any are given. Like
// EntityChart it is a read for sandbox acceptance only (spec 005 NFR-005):
// omnistat never reads its own writes back in normal operation.
//
// The API returns attribute_values either as a slug → value map (the default)
// or, verbose, as an array of {slug, value} objects; both are accepted.
func (c *Client) EntityValues(ctx context.Context, entityID string, slugs ...string) (map[string]string, error) {
	req := c.sdk.EntityAPI.GetEntity(ctx, entityID)
	if len(slugs) > 0 {
		// An array parameter declared `style: form, explode: false`, so the SDK
		// sends `fields=a,b`. A bare repeated `fields=` would keep only the last.
		req = req.Fields(slugs)
	}
	res, resp, err := req.Execute() //nolint:bodyclose // Execute drains and closes the body
	if err != nil {
		return nil, mapErr("get entity", resp, err)
	}
	out := map[string]string{}
	av := res.GetAttributeValues()
	if m := av.MapmapOfStringstring; m != nil {
		for k, v := range *m {
			out[k] = v
		}
	}
	if arr := av.ArrayOfEntityAttributeValue; arr != nil {
		for _, v := range *arr {
			if slug := v.GetSlug(); slug != "" {
				out[slug] = v.GetValue()
			}
		}
	}
	return out, nil
}
