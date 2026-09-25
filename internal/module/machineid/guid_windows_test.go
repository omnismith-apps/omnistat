//go:build windows

package machineid_test

import (
	"context"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/module/machineid"
)

// Spec 006 FR-002/FR-004: the real registry value is readable unprivileged on a
// Windows host (the CI runner) and yields a derived identity.
func TestDiscover_WindowsRealRegistry(t *testing.T) {
	id, err := machineid.New().Discover(context.Background(), "")
	if err != nil {
		t.Fatalf("machine GUID not readable: %v", err)
	}
	if id.Source != machineid.SourceWindows || len(id.Value) != 64 {
		t.Fatalf("identity: %+v", id)
	}
}
