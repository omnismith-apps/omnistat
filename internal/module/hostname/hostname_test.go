package hostname_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/hostname"
)

// FR-023: one text attribute `hostname` on the host template, valid manifest.
func TestManifest(t *testing.T) {
	m := hostname.New()
	if m.Name() != "hostname" {
		t.Fatalf("name %q", m.Name())
	}
	mf := m.Manifest()
	if err := manifest.Validate([]manifest.Manifest{mf}); err != nil {
		t.Fatal(err)
	}
	if len(mf.Attributes) != 1 {
		t.Fatalf("attributes: %+v", mf.Attributes)
	}
	a := mf.Attributes[0]
	if a.Key != hostname.AttributeKey || a.Slug != "hostname" || a.Name != "Hostname" || a.Kind != manifest.KindText || a.Template != "" {
		t.Fatalf("attribute: %+v", a)
	}
}

// FR-024: trimmed OS hostname; empty is a failure; FR-025: 5m default.
func TestCollect(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		err     error
		want    string
		wantErr string
	}{
		{name: "plain", host: "edge-fra-01", want: "edge-fra-01"},
		{name: "trimmed", host: "  edge-fra-01\n", want: "edge-fra-01"},
		{name: "fqdn kept", host: "edge-fra-01.example.net", want: "edge-fra-01.example.net"},
		{name: "empty", host: "", wantErr: "hostname is empty"},
		{name: "whitespace", host: " \n", wantErr: "hostname is empty"},
		{name: "os error", err: errors.New("boom"), wantErr: "boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := hostname.New()
			m.Hostname = func() (string, error) { return tc.host, tc.err }
			obs, err := m.Collect(context.Background())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(obs) != 1 || obs[0].Key != hostname.AttributeKey || obs[0].Value != tc.want {
				t.Fatalf("obs = %+v", obs)
			}
		})
	}
	var _ module.Provider = hostname.New()
	if hostname.New().DefaultInterval() != 5*time.Minute {
		t.Fatalf("interval %s", hostname.New().DefaultInterval())
	}
}

// The real hostname function returns something on the test machine.
func TestCollectReal(t *testing.T) {
	obs, err := hostname.New().Collect(context.Background())
	if err != nil || len(obs) != 1 || obs[0].Value == "" {
		t.Fatalf("real: %+v %v", obs, err)
	}
}
