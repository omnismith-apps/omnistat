package schema

import (
	"context"
	"errors"

	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// ErrAlreadyExists is returned (wrapped) by an API when a create is refused
// because an object with the same slug/value already exists — the signal for
// the fleet-race path (FR-024).
var ErrAlreadyExists = errors.New("already exists")

// API is the narrow, additive-only view of Omnismith that reconciliation
// needs. It deliberately has no delete, replace or rename method
// (constitution IV): the compiler is the guard.
type API interface {
	// ReadSchema returns the project's current schema in one call (FR-012).
	ReadSchema(ctx context.Context) (Current, error)
	// CreateTemplate creates a template and returns its id.
	CreateTemplate(ctx context.Context, p CreateTemplateParams) (id string, err error)
	// CreateAttribute creates an attribute, bound to TemplateIDs, and returns its id.
	CreateAttribute(ctx context.Context, p CreateAttributeParams) (id string, err error)
	// AddListOption appends one option to a list attribute and returns the new
	// list item's id. Callers keep that id rather than re-reading discovery for
	// it: the platform processes writes asynchronously, so discovery may not
	// show the option yet (spec 001 FR-025, spec 003 FR-012a).
	AddListOption(ctx context.Context, attributeID, value string) (id string, err error)
	// BindAttribute sets the full list of templates an attribute belongs to
	// (the platform call has replace semantics; callers pass existing ∪ new).
	BindAttribute(ctx context.Context, attributeID string, templateIDs []string) error
}

// CreateTemplateParams describes a template to create.
type CreateTemplateParams struct {
	Slug        string
	Name        string
	Description string
}

// CreateAttributeParams describes an attribute to create.
type CreateAttributeParams struct {
	Slug        string
	Name        string
	Description string
	Kind        manifest.Kind
	TemplateIDs []string
}
