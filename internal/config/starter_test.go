package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// yamlLine is a commented-out setting in the starter: "# " followed by YAML
// (a key or a list item, indentation kept). Prose starts with a capital letter.
var yamlLine = regexp.MustCompile(`^# ( *)([a-z_][a-z0-9_]*:( |$)|- )`)

// starterBlocks returns each block of the starter (blank-line separated) with
// its settings uncommented, skipping blocks that are prose only.
func starterBlocks(t *testing.T) []string {
	t.Helper()
	var blocks []string
	for _, block := range strings.Split(string(Starter), "\n\n") {
		var yaml []string
		for _, line := range strings.Split(block, "\n") {
			if yamlLine.MatchString(line) {
				yaml = append(yaml, strings.TrimPrefix(line, "# "))
			}
		}
		if len(yaml) > 0 {
			blocks = append(blocks, strings.Join(yaml, "\n")+"\n")
		}
	}
	return blocks
}

func loadText(t *testing.T, text string) (Settings, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "omnistat.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadWithDefault(path, "", func(string) string { return "" })
}

// 007 FR-014: the starter as shipped changes nothing — every default stays
// in force.
func TestStarter_LoadsToDefaults(t *testing.T) {
	got, err := loadText(t, string(Starter))
	if err != nil {
		t.Fatal(err)
	}
	want, err := LoadWithDefault("", "", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	got.Path = ""
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the starter must leave the defaults:\n got %+v\nwant %+v", got, want)
	}
}

// 007 FR-014: each setting block is valid uncommented alone, and all of them
// together (no key is shown twice).
func TestStarter_BlocksAreValid(t *testing.T) {
	blocks := starterBlocks(t)
	if len(blocks) < 8 {
		t.Fatalf("expected a block per setting group, found %d", len(blocks))
	}
	for _, b := range blocks {
		if _, err := loadText(t, b); err != nil {
			t.Errorf("block does not load uncommented:\n%s\n%v", b, err)
		}
	}
	if _, err := loadText(t, strings.Join(blocks, "")); err != nil {
		t.Errorf("all blocks together do not load: %v", err)
	}
}

// 007 FR-014: every setting of the config file is shown, so a new one cannot
// be forgotten; the token warning and the restart note are there.
func TestStarter_CoversEveryKey(t *testing.T) {
	text := string(Starter)
	for _, key := range yamlKeys(reflect.TypeFor[file]()) {
		if key == "access_token" {
			continue // named in prose: it must never be set here
		}
		if !regexp.MustCompile(`(?m)^# +` + key + `:`).MatchString(text) {
			t.Errorf("the starter does not show %q", key)
		}
	}
	for _, want := range []string{"OMNISMITH_ACCESS_TOKEN", "access_token", "restart"} {
		if !strings.Contains(text, want) {
			t.Errorf("the starter should mention %q", want)
		}
	}
}

// yamlKeys lists the yaml tags of t's fields, recursively through structs and
// map values.
func yamlKeys(t reflect.Type) []string {
	var keys []string
	for f := range t.Fields() {
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		keys = append(keys, tag)
		ft := f.Type
		for ft.Kind() == reflect.Map || ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			keys = append(keys, yamlKeys(ft)...)
		}
	}
	return keys
}
