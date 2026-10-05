package main

import (
	"testing"

	"github.com/omnismith-apps/omnistat/internal/manifest"
	"github.com/omnismith-apps/omnistat/internal/module"
)

// spec 012 FR-003: `ups` is off unless enabled; with every module on, the
// shipped manifests validate and resolve together (001 FR-004, 011 FR-003).
func TestRegistry_ShippedModules(t *testing.T) {
	r := registry()
	off, err := r.Enabled(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range off {
		if m.Name() == "ups" {
			t.Fatal("ups is enabled by default")
		}
	}
	all := map[string]bool{}
	for _, n := range r.Names() {
		all[n] = true
	}
	on, err := r.Enabled(all)
	if err != nil {
		t.Fatal(err)
	}
	ms := module.Manifests(on)
	if err := manifest.Validate(ms); err != nil {
		t.Fatal(err)
	}
	d, err := manifest.Resolve(ms, manifest.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	link, ok := d.HostLink("ups")
	if !ok || link.Display != "hostname" || link.Target != "host" {
		t.Fatalf("ups host link: %+v", link)
	}
}
