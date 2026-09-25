package service_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/service"
)

// 007 US-1/6, FR-024: the dry-run prints each step and its detail; the real
// run applies the steps, prints notes, never the detail, and stops at the
// first failure, naming it.
func TestPlan_DescribeAndApply(t *testing.T) {
	var applied []string
	p := &service.Plan{Keep: []string{"/etc/omnistat (your configuration)"}}
	p.Add("first", func(context.Context) (string, error) { applied = append(applied, "first"); return "a note", nil })
	p.AddDetailed("write the unit", "[Unit]\nDescription=x\n", func(context.Context) (string, error) {
		applied = append(applied, "unit")
		return "", nil
	})

	var dry bytes.Buffer
	p.Describe(&dry)
	want := "  would first\n  would write the unit\n      [Unit]\n      Description=x\n  would keep /etc/omnistat (your configuration)\n"
	if dry.String() != want || len(applied) != 0 {
		t.Fatalf("dry-run:\n%q\nwant\n%q (applied %v)", dry.String(), want, applied)
	}

	var out bytes.Buffer
	if err := p.Apply(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(applied, ",") != "first,unit" || !strings.Contains(out.String(), "        a note") || strings.Contains(out.String(), "Description=x") {
		t.Fatalf("apply: %v\n%s", applied, out.String())
	}

	boom := errors.New("boom")
	f := &service.Plan{}
	f.Add("fails", func(context.Context) (string, error) { return "", boom })
	f.Add("never", func(context.Context) (string, error) { t.Fatal("ran after a failure"); return "", nil })
	if err := f.Apply(context.Background(), &bytes.Buffer{}); !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "fails: ") {
		t.Fatalf("%v", err)
	}
}

// 006/007 FR-011: the watch reports the last state after settle, and fails at
// once when the check does.
func TestWatch(t *testing.T) {
	n := 0
	note, err := service.Watch(context.Background(), 20*time.Millisecond, time.Millisecond, func(context.Context) (string, error) {
		n++
		return "running", nil
	})
	if err != nil || note != "service is running" || n < 2 {
		t.Fatalf("%q %v after %d checks", note, err, n)
	}
	dead := errors.New("stopped")
	if _, err := service.Watch(context.Background(), time.Hour, time.Millisecond, func(context.Context) (string, error) { return "", dead }); !errors.Is(err, dead) {
		t.Fatalf("%v", err)
	}
}
