package omni

import (
	"context"
	"fmt"

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
