package schema_test

import (
	"context"
	"sync"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/omni"
	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
	"github.com/omnismith-apps/omnistat/internal/schema"
)

// US-5/1, FR-024: N hosts applying at once against an empty project converge on
// one template and one attribute per manifest entry, and all succeed.
func TestApply_ConcurrentConverges(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	const hosts = 6

	var wg sync.WaitGroup
	errs := make([]error, hosts)
	results := make([]schema.Result, hosts)
	for i := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := omni.New(omni.Settings{BaseURL: srv.URL, Token: omnitest.Token, ProjectID: omnitest.ProjectID, Retries: 0})
			if err != nil {
				errs[i] = err
				return
			}
			cur, err := c.ReadSchema(context.Background())
			if err != nil {
				errs[i] = err
				return
			}
			results[i], errs[i] = schema.Apply(context.Background(), c, desired(), cur, quiet)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("host %d: %v", i, err)
		}
		if len(results[i].Resolved.Attributes) != 3 || results[i].Resolved.Templates["host"] == "" {
			t.Errorf("host %d resolved incomplete: %+v", i, results[i].Resolved)
		}
	}
	if tpls := srv.Templates(); len(tpls) != 1 || len(tpls[0].AttributeSlugs) != 3 {
		t.Fatalf("templates: %+v", tpls)
	}
	if attrs := srv.Attributes(); len(attrs) != 3 {
		t.Fatalf("attributes: %+v", attrs)
	}
	for _, a := range srv.Attributes() {
		if a.Slug == "cpu_arch" && len(a.Options) != 2 {
			t.Fatalf("options: %+v", a)
		}
	}
}
