package omni

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	omnismithsdk "github.com/omnismith-sdk/go"

	"github.com/omnismith-apps/omnistat/internal/publish"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

// Sentinel errors callers branch on with errors.Is. An *APIError matches the
// sentinel that corresponds to its status/code; schema.ErrAlreadyExists is
// matched when the platform refused a create because the slug/value is taken.
var (
	ErrUnauthorized = errors.New("unauthorized: the access token is missing, invalid or expired")
	ErrForbidden    = errors.New("forbidden: the token lacks permission for this operation")
	ErrStaleGrant   = errors.New("forbidden: stale project grant, refresh the credential")
	ErrProjectDeny  = errors.New("forbidden: the token has no access to this project")
	ErrNoProject    = errors.New("no project selected: set OMNISMITH_PROJECT_ID")
	ErrNotFound     = errors.New("not found")
	ErrValidation   = errors.New("validation failed")
)

// APIError is any non-2xx answer from Omnismith, decoded from its RFC 7807 body.
type APIError struct {
	Status int
	Code   string
	Title  string
	Detail string
	// Fields holds the 422 field errors, keyed by field name.
	Fields map[string][]string
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "omnismith: HTTP %d", e.Status)
	if e.Title != "" {
		fmt.Fprintf(&b, " %s", e.Title)
	}
	if e.Code != "" {
		fmt.Fprintf(&b, " (%s)", e.Code)
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, ": %s", e.Detail)
	}
	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "; %s: %s", k, strings.Join(e.Fields[k], ", "))
		}
	}
	return b.String()
}

// Is maps the error onto the sentinels.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized
	case ErrStaleGrant:
		return e.Status == http.StatusForbidden && e.Code == "stale_project_grant"
	case ErrProjectDeny:
		return e.Status == http.StatusForbidden && e.Code == "project_access_denied"
	case ErrForbidden:
		return e.Status == http.StatusForbidden
	case ErrNoProject:
		return e.Status == http.StatusConflict && e.Code == "no_project_selected"
	case ErrNotFound, publish.ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrValidation, publish.ErrRejected:
		return e.Status == http.StatusUnprocessableEntity
	case schema.ErrAlreadyExists:
		return e.alreadyExists()
	}
	return false
}

// RejectedFields implements publish.Rejected.
func (e *APIError) RejectedFields() map[string][]string { return e.Fields }

// alreadyExists recognises "this slug/value is taken" answers: a 409 that is
// not the no-project code, or a 422 whose field errors mention "taken".
func (e *APIError) alreadyExists() bool {
	if e.Status == http.StatusConflict && e.Code != "no_project_selected" {
		return true
	}
	if e.Status != http.StatusUnprocessableEntity {
		return false
	}
	for _, msgs := range e.Fields {
		for _, m := range msgs {
			if strings.Contains(strings.ToLower(m), "already") || strings.Contains(strings.ToLower(m), "taken") {
				return true
			}
		}
	}
	return false
}

// mapErr converts whatever the SDK returned into an *APIError (or passes
// transport errors through, wrapped with the operation name).
func mapErr(op string, resp *http.Response, err error) error {
	if err == nil {
		return nil
	}
	var gen *omnismithsdk.GenericOpenAPIError
	if !errors.As(err, &gen) {
		return fmt.Errorf("%s: %w", op, err)
	}
	ae := &APIError{Title: gen.Error()}
	if resp != nil {
		ae.Status = resp.StatusCode
	}
	var body struct {
		Title  string              `json:"title"`
		Status int                 `json:"status"`
		Detail string              `json:"detail"`
		Code   string              `json:"code"`
		Errors map[string][]string `json:"errors"`
	}
	if json.Unmarshal(gen.Body(), &body) == nil {
		if body.Title != "" {
			ae.Title = body.Title
		}
		if ae.Status == 0 {
			ae.Status = body.Status
		}
		ae.Detail, ae.Code, ae.Fields = body.Detail, body.Code, body.Errors
	}
	return fmt.Errorf("%s: %w", op, ae)
}
