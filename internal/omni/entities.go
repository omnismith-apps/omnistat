package omni

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	omnismithsdk "github.com/omnismith-sdk/go"

	"github.com/omnismith-apps/omnistat/internal/identity"
)

var _ identity.API = (*Client)(nil)

// FindEntities implements identity.API: exact match on one attribute, oldest
// first, with each record's external key.
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
		out = append(out, identity.EntitySummary{ID: e.GetId(), CreatedAt: e.GetCreatedAt(), Key: e.GetExternalKey()})
	}
	return out, nil
}

// EntityByKey implements identity.API: the live record of the template
// holding key (spec 011 FR-011). Only the id is wanted, so the projection asks
// for the standard fields only.
func (c *Client) EntityByKey(ctx context.Context, templateSlug, key string) (string, bool, error) {
	res, resp, err := c.sdk.EntityAPI.GetEntityByKey(ctx, templateSlug).Key(key).Execute() //nolint:bodyclose // Execute drains and closes the body
	if id, ok := idDespiteDecodeError(resp, err); ok {
		return id, true, nil
	}
	if err != nil {
		err = mapErr("get entity by key", resp, err)
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return res.GetId(), true, nil
}

// idDespiteDecodeError reads the id of a 2xx entity response the SDK could
// not decode. SDK v1.0.18's oneOf decoder for attribute_values rejects an
// empty object, which is what the API returns for a record with no values
// (see docs/reference/omnismith-api-notes.md).
func idDespiteDecodeError(resp *http.Response, err error) (string, bool) {
	var gen *omnismithsdk.GenericOpenAPIError
	if err == nil || resp == nil || resp.StatusCode/100 != 2 || !errors.As(err, &gen) {
		return "", false
	}
	var body struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(gen.Body(), &body) != nil || body.ID == "" {
		return "", false
	}
	return body.ID, true
}

// UpsertByKey implements identity.API: one atomic create-or-update by key
// (spec 011 FR-011). A 409 is recognisable as identity.ErrKeyTaken.
func (c *Client) UpsertByKey(ctx context.Context, templateSlug, key string, attrs map[string]any) (string, bool, error) {
	values, err := plainValues("upsert entity", attrs)
	if err != nil {
		return "", false, err
	}
	req := omnismithsdk.NewUpsertEntityByKeyRequest(key, values)
	res, resp, err := c.sdk.EntityAPI.UpsertEntityByKey(ctx, templateSlug).UpsertEntityByKeyRequest(*req).Execute() //nolint:bodyclose // Execute drains and closes the body
	if err != nil {
		return "", false, mapErr("upsert entity by key", resp, err)
	}
	return res.GetId(), res.GetCreated(), nil
}

// SetEntityKey implements identity.API: a partial update carrying only the
// external key (spec 011 FR-012).
func (c *Client) SetEntityKey(ctx context.Context, entityID, key string) error {
	req := omnismithsdk.NewUpdateEntityRequest()
	req.SetExternalKey(key)
	resp, err := c.sdk.EntityAPI.UpdateEntity(ctx, entityID).UpdateEntityRequest(*req).Execute() //nolint:bodyclose // Execute drains and closes the body
	return mapErr("set entity key", resp, err)
}

// CreateEntity creates an entity without a key. Resolution no longer uses
// it; sandbox tests seed legacy host entities with it (spec 011 NFR-006).
func (c *Client) CreateEntity(ctx context.Context, templateSlug string, attrs map[string]any) (string, error) {
	values, err := plainValues("create entity", attrs)
	if err != nil {
		return "", err
	}
	req := omnismithsdk.NewCreateEntityRequest(values)
	res, resp, err := c.sdk.EntityAPI.CreateEntity(ctx, templateSlug).CreateEntityRequest(*req).Execute() //nolint:bodyclose // Execute drains and closes the body
	if err != nil {
		return "", mapErr("create entity", resp, err)
	}
	return res.GetId(), nil
}

// plainValues renders attribute values as the SDK's scalar union: strings,
// numbers or booleans according to their Go type.
func plainValues(op string, attrs map[string]any) (map[string]omnismithsdk.EntityAttributesInputValue, error) {
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
			return nil, fmt.Errorf("%s: unsupported value type %T for %s", op, v, slug)
		}
		values[slug] = in
	}
	return values, nil
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
