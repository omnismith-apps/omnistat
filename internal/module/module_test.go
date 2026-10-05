package module_test

import (
	"context"
	"errors"
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

// configurable is a fixture module with one setting.
type configurable struct {
	module.Static
	got map[string]string
}

func (c *configurable) Settings() []string { return []string{"address"} }
func (c *configurable) Configure(s map[string]string) error {
	if s["address"] == "bad" {
		return errors.New("address is bad")
	}
	c.got = s
	return nil
}

// spec 012 FR-005: settings reach the module that accepts them, disabled or
// not; unknown keys, modules without settings, unknown modules and the
// module's own refusal are errors.
func TestRegistry_Configure(t *testing.T) {
	c := &configurable{Static: module.Static{M: manifest.Manifest{Module: "dev"}}}
	r := module.NewRegistry()
	r.Register(c, module.DisabledByDefault())
	r.Register(module.Static{M: manifest.Manifest{Module: "plain"}})

	if err := r.Configure(map[string]map[string]string{"dev": {"address": "x:1"}}); err != nil || c.got["address"] != "x:1" {
		t.Fatalf("configure: %v %v", err, c.got)
	}
	if err := r.Configure(nil); err != nil || len(c.got) != 0 {
		t.Fatalf("no settings must reach the module as defaults: %v %v", err, c.got)
	}
	for name, settings := range map[string]map[string]map[string]string{
		"unknown key":       {"dev": {"addr": "x"}},
		"module refuses":    {"dev": {"address": "bad"}},
		"no settings":       {"plain": {"address": "x"}},
		"unregistered name": {"ghost": {"address": "x"}},
	} {
		if err := r.Configure(settings); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
