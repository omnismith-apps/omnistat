package omni

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	omnismithsdk "github.com/omnismith-sdk/go"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

// DefaultBaseURL is the production API.
const DefaultBaseURL = "https://api.omnismith.io/v1"

// Settings configures a Client. Token and ProjectID are secrets/tenant ids and
// must never be logged.
type Settings struct {
	BaseURL   string
	Token     string
	ProjectID string
	Timeout   time.Duration // per attempt; 0 = 15s
	Retries   int           // transient retries; <0 = none
	Version   string        // for the User-Agent
	Logger    *slog.Logger
	// Transport is the base round tripper (nil = http.DefaultTransport).
	Transport http.RoundTripper
}

// Client implements schema.API (and, later, the entity operations) on top of
// the official SDK.
type Client struct {
	sdk *omnismithsdk.APIClient
	log *slog.Logger
}

// New builds a client. It performs no network I/O.
func New(s Settings) (*Client, error) {
	if strings.TrimSpace(s.Token) == "" {
		return nil, errors.New("omnismith: access token is empty")
	}
	if strings.TrimSpace(s.ProjectID) == "" {
		return nil, errors.New("omnismith: project id is empty")
	}
	if s.BaseURL == "" {
		s.BaseURL = DefaultBaseURL
	}
	if s.Timeout == 0 {
		s.Timeout = 15 * time.Second
	}
	if s.Retries < 0 {
		s.Retries = 0
	}
	if s.Version == "" {
		s.Version = "dev"
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	cfg := omnismithsdk.NewConfiguration()
	cfg.Servers = omnismithsdk.ServerConfigurations{{URL: strings.TrimRight(s.BaseURL, "/")}}
	cfg.AddDefaultHeader("Authorization", "Bearer "+s.Token)
	cfg.AddDefaultHeader("X-Omnismith-Project-Id", s.ProjectID)
	cfg.UserAgent = "omnistat/" + s.Version
	cfg.HTTPClient = &http.Client{Transport: newTransport(s.Transport, s.Timeout, s.Retries, cfg.UserAgent)}
	return &Client{sdk: omnismithsdk.NewAPIClient(cfg), log: s.Logger}, nil
}

var _ schema.API = (*Client)(nil)

// ReadSchema implements schema.API (FR-012). Templates or attributes without
// a slug cannot be matched and are skipped.
func (c *Client) ReadSchema(ctx context.Context) (schema.Current, error) {
	res, resp, err := c.sdk.SchemaAPI.GetProjectSchema(ctx).Execute() //nolint:bodyclose // Execute drains and closes the body
	if err != nil {
		return schema.Current{}, mapErr("read schema", resp, err)
	}
	cur := schema.Current{Templates: map[string]schema.CurrentTemplate{}, Attributes: map[string]schema.CurrentAttribute{}}
	for _, t := range res.Templates {
		slug := t.GetSlug()
		if slug == "" {
			continue
		}
		ct := schema.CurrentTemplate{ID: t.GetId(), Slug: slug, Name: t.GetName()}
		for _, a := range t.Attributes {
			ct.AttributeIDs = append(ct.AttributeIDs, a.GetId())
		}
		cur.Templates[slug] = ct
	}
	for _, a := range res.Attributes {
		slug := a.GetSlug()
		if slug == "" {
			continue
		}
		ca := schema.CurrentAttribute{ID: a.GetId(), Slug: slug, Name: a.GetName(), Type: a.GetType()}
		for _, o := range a.GetOptions() {
			ca.Options = append(ca.Options, o.GetValue())
		}
		cur.Attributes[slug] = ca
	}
	return cur, nil
}

// MyPermissions returns the permission strings of the token (FR-027 pre-flight).
func (c *Client) MyPermissions(ctx context.Context) ([]string, error) {
	res, resp, err := c.sdk.AuthAPI.GetMyPermissions(ctx).Execute() //nolint:bodyclose // Execute drains and closes the body
	if err != nil {
		return nil, mapErr("read permissions", resp, err)
	}
	return res.GetData(), nil
}

// CreateTemplate implements schema.API.
func (c *Client) CreateTemplate(ctx context.Context, p schema.CreateTemplateParams) (string, error) {
	req := omnismithsdk.NewCreateTemplateRequest(p.Name)
	req.SetSlug(p.Slug)
	if p.Description != "" {
		req.SetDescription(p.Description)
	}
	res, resp, err := c.sdk.TemplatesAPI.CreateTemplate(ctx).CreateTemplateRequest(*req).Execute() //nolint:bodyclose // Execute drains and closes the body
	if err != nil {
		return "", mapErr("create template "+p.Slug, resp, err)
	}
	return res.GetId(), nil
}

// CreateAttribute implements schema.API.
func (c *Client) CreateAttribute(ctx context.Context, p schema.CreateAttributeParams) (string, error) {
	at, dt, err := enums(p.Kind)
	if err != nil {
		return "", err
	}
	req := omnismithsdk.NewCreateAttributeRequest(p.Name, at, dt)
	req.SetSlug(p.Slug)
	if p.Description != "" {
		req.SetDescription(p.Description)
	}
	if len(p.TemplateIDs) > 0 {
		req.SetTemplateIds(p.TemplateIDs)
	}
	res, resp, err := c.sdk.AttributesAPI.CreateAttribute(ctx).CreateAttributeRequest(*req).Execute() //nolint:bodyclose // Execute drains and closes the body
	if err != nil {
		return "", mapErr("create attribute "+p.Slug, resp, err)
	}
	return res.GetId(), nil
}

// AddListOption implements schema.API.
func (c *Client) AddListOption(ctx context.Context, attributeID, value string) error {
	req := omnismithsdk.NewAddListItemRequest(value)
	_, resp, err := c.sdk.AttributesAPI.CreateAttributeItem(ctx, attributeID).AddListItemRequest(*req).Execute() //nolint:bodyclose // Execute drains and closes the body
	return mapErr(fmt.Sprintf("add option %q", value), resp, err)
}

// BindAttribute implements schema.API. The platform call replaces the
// attribute's template list, so templateIDs must be the full desired set.
func (c *Client) BindAttribute(ctx context.Context, attributeID string, templateIDs []string) error {
	req := omnismithsdk.NewPatchAttributeRequest()
	req.SetTemplateIds(templateIDs)
	resp, err := c.sdk.AttributesAPI.PatchAttribute(ctx, attributeID).PatchAttributeRequest(*req).Execute() //nolint:bodyclose // Execute drains and closes the body
	return mapErr("bind attribute", resp, err)
}

// enums maps a manifest kind to the platform's attribute_type / data_type.
func enums(k manifest.Kind) (attributeType, dataType int32, err error) {
	switch k {
	case manifest.KindText:
		return 0, 0, nil
	case manifest.KindNumber:
		return 0, 1, nil
	case manifest.KindBoolean:
		return 0, 2, nil
	case manifest.KindDatetime:
		return 0, 3, nil
	case manifest.KindDate:
		return 0, 4, nil
	case manifest.KindMetric:
		return 1, 1, nil
	case manifest.KindList:
		return 2, 0, nil
	}
	return 0, 0, fmt.Errorf("omnismith: kind %q cannot be created", k)
}
