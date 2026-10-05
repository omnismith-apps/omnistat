//go:build sandbox && linux

package cli_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/cli"
	"github.com/omnismith-apps/omnistat/internal/config"
	"github.com/omnismith-apps/omnistat/internal/identity"
	"github.com/omnismith-apps/omnistat/internal/module"
	"github.com/omnismith-apps/omnistat/internal/module/hostname"
	"github.com/omnismith-apps/omnistat/internal/module/machineid"
	"github.com/omnismith-apps/omnistat/internal/module/ups"
	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

// TestSandbox_UPS proves specs 011 and 012 end to end against a real project
// (011 NFR-006, 012 NFR-006), with a fake apcupsd: the schema gains the ups
// template and its host link; one run creates the UPS record holding the
// serial as its key, linked to the host; its values read back; a second run
// reuses it. Nothing is deleted (constitution IV).
//
//	make sandbox
func TestSandbox_UPS(t *testing.T) {
	s, err := config.Load("", os.Getenv)
	if err != nil || s.RequireAPI() != nil {
		t.Skip("sandbox credentials not set")
	}
	serial := "SANDBOX-" + time.Now().UTC().Format("20060102T150405")
	addr := serveNIS(t, strings.Replace(nisReply, "SERIALNO : 4B1234P56789", "SERIALNO : "+serial, 1))
	cfg := t.TempDir() + "/omnistat.yaml"
	if err := os.WriteFile(cfg, []byte("modules:\n  ups:\n    enabled: true\n    address: "+addr+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := module.NewRegistry()
	reg.Register(machineid.New(), module.Required())
	hn := hostname.New()
	hn.Hostname = func() (string, error) { return "sandbox-ups-host", nil }
	reg.Register(hn)
	reg.Register(ups.New(), module.DisabledByDefault())
	app := &cli.App{Registry: reg, Version: "sandbox"}
	env := func(k string) string {
		if k == config.EnvIdentity {
			return "sandbox-ups-host"
		}
		return os.Getenv(k)
	}
	runOnce := func() string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := app.Run(context.Background(), []string{"--config", cfg, "run"}, &out, &errb, env); code != 0 || !strings.Contains(out.String(), "and 1 module entities") {
			t.Fatalf("run: code=%d\n%s%s", code, out.String(), errb.String())
		}
		return out.String()
	}
	t.Log(strings.TrimSpace(runOnce()))

	api, err := omni.New(omni.Settings{BaseURL: s.BaseURL, Token: s.Token, ProjectID: s.ProjectID, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The key does not lag (spec 011): the lookup sees the record at once.
	id, found, err := api.EntityByKey(ctx, "ups", serial)
	if err != nil || !found {
		t.Fatalf("UPS record by key: %v %v", found, err)
	}
	host, found, err := api.EntityByKey(ctx, "host", "sandbox-ups-host")
	if err != nil || !found {
		t.Fatalf("host record by key: %v %v", found, err)
	}
	cur := eventually(t, "ups schema", api.ReadSchema, func(c schema.Current) bool {
		return c.Attributes["ups_host"].RefTemplateID != "" && c.Templates["ups"].ID != ""
	})
	if cur.Attributes["ups_host"].RefTemplateID != cur.Templates["host"].ID {
		t.Fatalf("ups_host targets %s, want the host template %s", cur.Attributes["ups_host"].RefTemplateID, cur.Templates["host"].ID)
	}
	vals := eventually(t, "UPS values", func(ctx context.Context) (map[string]string, error) {
		return api.EntityValues(ctx, id, "ups_on_battery", "ups_name", "ups_serial", "ups_host", "ups_last_transfer_reason")
	}, func(v map[string]string) bool { return v["ups_name"] == "garage" })
	t.Logf("UPS %s values: %v (host %s)", id, vals, host)
	if vals["ups_serial"] != serial || vals["ups_last_transfer_reason"] != "Low line voltage" || vals["ups_host"] == "" {
		t.Fatalf("values: %v", vals)
	}
	charge := cur.Attributes["ups_battery_charge_pct"].ID
	eventually(t, "battery charge series", func(ctx context.Context) (map[string][]omni.ChartPoint, error) {
		return api.EntityChart(ctx, id, []string{charge}, time.Now().Add(-time.Hour), time.Now().Add(time.Minute), "1 second", "")
	}, func(m map[string][]omni.ChartPoint) bool { return len(m[charge]) > 0 && m[charge][0].Value == 95 })

	runOnce()
	if again, _, err := api.EntityByKey(ctx, "ups", serial); err != nil || again != id {
		t.Fatalf("second run: %s %v (first %s)", again, err, id)
	}
	dups := eventually(t, "one UPS record", func(ctx context.Context) ([]identity.EntitySummary, error) {
		return api.FindEntities(ctx, cur.Templates["ups"].ID, "ups_serial", serial)
	}, func(es []identity.EntitySummary) bool { return len(es) > 0 })
	if len(dups) != 1 {
		t.Fatalf("UPS records with serial %s: %+v", serial, dups)
	}
}
