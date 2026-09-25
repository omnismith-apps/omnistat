package systemd_test

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/omnismith-apps/omnistat/internal/systemd"
)

// FR-022: stderr is the journal only when JOURNAL_STREAM names that very
// stream, so a redirected stderr keeps plain lines.
func TestJournalStream(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	match := fmt.Sprintf("%d:%d", st.Dev, st.Ino)
	for value, want := range map[string]bool{
		match:                                  true,
		fmt.Sprintf("%d:%d", st.Dev, st.Ino+1): false,
		"":                                     false,
		"garbage":                              false,
		"1:":                                   false,
	} {
		if got := systemd.JournalStream(f, func(k string) string {
			if k == "JOURNAL_STREAM" {
				return value
			}
			return ""
		}); got != want {
			t.Errorf("JOURNAL_STREAM=%q: got %t", value, got)
		}
	}
}
