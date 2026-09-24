package omni_test

import (
	"context"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/omni/omnitest"
)

// spec 005 NFR-005: acceptance reads a dimension back from the entity.
func TestEntityValues(t *testing.T) {
	srv := omnitest.New()
	defer srv.Close()
	srv.AddAttribute("mem_total_mib", "number")
	srv.AddAttribute("hostname", "string")
	srv.AddTemplate("host", "Host", "mem_total_mib", "hostname")
	id := srv.AddEntity("host", map[string]any{"mem_total_mib": 31820, "hostname": "box"}, "")
	c := newClient(t, srv, omnitest.Token, omnitest.ProjectID)

	got, err := c.EntityValues(context.Background(), id, "mem_total_mib")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["mem_total_mib"] != "31820" {
		t.Fatalf("projected read: %v", got)
	}
	all, err := c.EntityValues(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if all["hostname"] != "box" || all["mem_total_mib"] != "31820" {
		t.Fatalf("full read: %v", all)
	}
	// The verbose array shape decodes to the same map.
	srv.VerboseEntities = true
	arr, err := c.EntityValues(context.Background(), id, "mem_total_mib", "hostname")
	if err != nil {
		t.Fatal(err)
	}
	if len(arr) != 2 || arr["hostname"] != "box" || arr["mem_total_mib"] != "31820" {
		t.Fatalf("array shape: %v", arr)
	}
	if _, err := c.EntityValues(context.Background(), "nope"); err == nil {
		t.Fatal("unknown entity must fail")
	}
}
