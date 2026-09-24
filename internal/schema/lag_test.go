package schema_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/schema"
	"github.com/omnismith-apps/omnistat/internal/settle"
)

// noSleep is a settle policy that waits for nothing but records each wait.
func noSleep(waits *[]time.Duration) settle.Policy {
	return settle.Policy{
		Delays: settle.Default().Delays,
		Sleep: func(ctx context.Context, d time.Duration) error {
			*waits = append(*waits, d)
			return ctx.Err()
		},
	}
}

func writes(srv *omnitest.Server) []string {
	var out []string
	for _, r := range srv.Requests() {
		if r.Method != http.MethodGet {
			out = append(out, r.Method+" "+r.Path+" "+r.Body)
		}
	}
	return out
}

// FR-025 on an asynchronous platform: discovery lags behind this run's own
// writes. The ids a run learned from its write responses — templates,
// attributes, and list items (spec 003 FR-012a) — must survive the stale
// verification read, and nothing this run created may be created again
// because the read did not show it yet.
func TestApply_StaleDiscoveryKeepsOwnWrites(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	api := client(t, srv)
	cur := mustRead(t, api)
	srv.SchemaLag = 100 // discovery shows none of this run's writes

	var waits []time.Duration
	res, err := schema.ApplyWith(context.Background(), api, desired(), cur, quiet, noSleep(&waits))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	r := res.Resolved
	if r.Templates["host"] == "" || len(r.Attributes) != 3 {
		t.Fatalf("ids learned from writes were lost: %+v", r)
	}
	if r.ListItems["cpu_arch"]["amd64"] == "" || r.ListItems["cpu_arch"]["arm64"] == "" {
		t.Fatalf("list item ids must come from the create responses: %+v", r.ListItems)
	}
	// One write per object: the stale read did not trigger re-creation.
	seen := map[string]int{}
	for _, w := range writes(srv) {
		seen[w]++
	}
	for w, n := range seen {
		if n > 1 && !strings.HasPrefix(w, "PATCH") {
			t.Errorf("%s sent %d times", w, n)
		}
	}
	if len(srv.Templates()) != 1 || len(srv.Attributes()) != 3 {
		t.Fatalf("server state: %+v %+v", srv.Templates(), srv.Attributes())
	}
	if a := srv.Attributes(); strings.Join(a[0].Options, ",") != "amd64,arm64" {
		t.Fatalf("options: %v", a[0].Options)
	}
}

// FR-024 on an asynchronous platform: another host created the attribute
// moments earlier, so the create is refused, but discovery does not show the
// object yet. The re-read waits for it rather than failing the run.
func TestApply_RaceObjectAppearsLate(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	api := client(t, srv)
	cur := mustRead(t, api)
	srv.SchemaLag = 2

	var once sync.Once
	srv.Before = func(r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/attributes" {
			once.Do(func() {
				srv.LagSchemaLocked()
				srv.AddAttributeLocked("cpu_model", "string")
			})
		}
	}
	var waits []time.Duration
	res, err := schema.ApplyWith(context.Background(), api, desired(), cur, quiet, noSleep(&waits))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(waits) == 0 {
		t.Fatal("the re-read must have waited for the object to appear")
	}
	if got := doneTypes(res); !strings.Contains(got, "create_attribute:cpu_model(skipped)") {
		t.Fatalf("expected the raced create to be skipped: %s", got)
	}
	if res.Resolved.Attributes["cpu_model"] == "" {
		t.Fatalf("resolved: %+v", res.Resolved)
	}
}

// FR-024: an object that never appears within the budget is still the
// original error — the wait is bounded.
func TestApply_RaceObjectNeverAppears(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	api := client(t, srv)
	cur := mustRead(t, api)
	srv.FailNext(omnitest.Fault{Method: "POST", PathPrefix: "/templates", Status: 409, Body: `{"title":"Conflict","status":409}`})

	var waits []time.Duration
	_, err := schema.ApplyWith(context.Background(), api, desired(), cur, quiet, noSleep(&waits))
	if !errors.Is(err, schema.ErrAlreadyExists) {
		t.Fatalf("want the original already-exists error, got %v", err)
	}
	if len(waits) != len(settle.Default().Delays) {
		t.Fatalf("waited %v; want the whole budget, once", waits)
	}
}
