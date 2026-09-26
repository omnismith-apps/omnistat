package module_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/moduletest"
)

func names(mods []module.Module) string {
	var out []string
	for _, m := range mods {
		out = append(out, m.Name())
	}
	return strings.Join(out, ",")
}

// FR-006 / US-4: defaults, switches, unknown names, required modules.
func TestRegistry_Enabled(t *testing.T) {
	r := moduletest.Registry()
	if got := strings.Join(r.Names(), ","); got != "ident,probe,volume" {
		t.Fatalf("names: %s", got)
	}
	mods, err := r.Enabled(nil)
	if err != nil || names(mods) != "ident,probe" {
		t.Fatalf("defaults: %s %v", names(mods), err)
	}
	mods, err = r.Enabled(map[string]bool{"volume": true, "probe": false})
	if err != nil || names(mods) != "ident,volume" {
		t.Fatalf("switched: %s %v", names(mods), err)
	}
	_, err = r.Enabled(map[string]bool{"gpu": true})
	if err == nil || !strings.Contains(err.Error(), `unknown module "gpu" (known: ident, probe, volume)`) {
		t.Fatalf("unknown: %v", err)
	}
	_, err = r.Enabled(map[string]bool{"ident": false})
	if err == nil || !strings.Contains(err.Error(), `module "ident" is required`) {
		t.Fatalf("required: %v", err)
	}
	if ms := module.Manifests(mods); len(ms) != 2 || ms[1].Module != "volume" {
		t.Fatalf("manifests: %+v", ms)
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	r := module.NewRegistry()
	r.Register(moduletest.Probe())
	r.Register(moduletest.Probe())
}

// Fixtures must themselves be valid manifests.
func TestFixturesValidate(t *testing.T) {
	mods, _ := moduletest.Registry().Enabled(map[string]bool{"volume": true})
	if err := manifest.Validate(module.Manifests(mods)); err != nil {
		t.Fatal(err)
	}
}

// Spec 003 FR-001: a provider is optional and discovered by ProviderOf.
func TestProviderOf(t *testing.T) {
	if _, ok := module.ProviderOf(moduletest.Probe()); ok {
		t.Fatal("Static must not be a provider")
	}
	p := moduletest.WithProvider(moduletest.Probe(), 10*time.Second, func(context.Context) ([]module.Observation, error) {
		return []module.Observation{{Key: "usage", Value: 42.0}}, nil
	})
	prov, ok := module.ProviderOf(p)
	if !ok {
		t.Fatal("Provided must be a provider")
	}
	if prov.DefaultInterval() != 10*time.Second {
		t.Fatalf("interval %s", prov.DefaultInterval())
	}
	obs, err := prov.Collect(context.Background())
	if err != nil || len(obs) != 1 || obs[0].Key != "usage" {
		t.Fatalf("collect: %+v %v", obs, err)
	}
	if p.Name() != "probe" || len(p.Manifest().Attributes) != 3 {
		t.Fatalf("manifest lost: %s %+v", p.Name(), p.Manifest())
	}
	if moduletest.WithProvider(moduletest.Probe(), 0, nil).DefaultInterval() != time.Minute {
		t.Fatal("zero interval must default to a minute")
	}
}
