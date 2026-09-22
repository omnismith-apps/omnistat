package collect_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/omnismith-apps/omnistat/internal/collect"
	"github.com/omnismith-apps/omnistat/internal/manifest"
)

// Spec 003 FR-002: values are typed by kind; anything else is rejected.
func TestValidate(t *testing.T) {
	ts := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	attr := func(k manifest.Kind, opts ...string) manifest.DesiredAttribute {
		return manifest.DesiredAttribute{Slug: "x", Kind: k, Options: opts, Module: "m", Key: "k"}
	}
	tests := []struct {
		name    string
		attr    manifest.DesiredAttribute
		in      any
		want    any
		wantErr string
	}{
		{"text", attr(manifest.KindText), "edge", "edge", ""},
		{"text from number", attr(manifest.KindText), 42, nil, "text wants a string"},
		{"number int", attr(manifest.KindNumber), 8, float64(8), ""},
		{"number int64", attr(manifest.KindNumber), int64(1 << 40), float64(1 << 40), ""},
		{"number uint8", attr(manifest.KindNumber), uint8(7), float64(7), ""},
		{"number float32", attr(manifest.KindNumber), float32(1.5), float64(1.5), ""},
		{"number string", attr(manifest.KindNumber), "8", nil, "number wants a number"},
		{"number NaN", attr(manifest.KindNumber), math.NaN(), nil, "not finite"},
		{"metric float", attr(manifest.KindMetric), 24.5, 24.5, ""},
		{"metric inf", attr(manifest.KindMetric), math.Inf(1), nil, "not finite"},
		{"boolean", attr(manifest.KindBoolean), true, true, ""},
		{"boolean string", attr(manifest.KindBoolean), "true", nil, "boolean wants a bool"},
		{"date", attr(manifest.KindDate), ts, ts, ""},
		{"datetime", attr(manifest.KindDatetime), ts, ts, ""},
		{"datetime zero", attr(manifest.KindDatetime), time.Time{}, nil, "zero time"},
		{"datetime string", attr(manifest.KindDatetime), "2026-09-22", nil, "datetime wants a time"},
		{"list ok", attr(manifest.KindList, "amd64", "arm64"), "arm64", "arm64", ""},
		{"list unknown", attr(manifest.KindList, "amd64", "arm64"), "riscv", nil, `"riscv" is not one of amd64, arm64`},
		{"list case", attr(manifest.KindList, "amd64"), "AMD64", nil, "is not one of"},
		{"nil", attr(manifest.KindText), nil, nil, "nil value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := collect.Validate(tc.attr, tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt, ok := tc.want.(time.Time); ok {
				if !got.(time.Time).Equal(tt) {
					t.Fatalf("got %v want %v", got, tt)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}
