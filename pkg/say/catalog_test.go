package say

import (
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	action = regexp.MustCompile(`\{\{[^}]*\}\}`)
	field  = regexp.MustCompile(`\.[A-Za-z]\w*`)
)

func shipped(t *testing.T, tag string) map[string]string {
	t.Helper()
	raw, err := files.ReadFile(path.Join("messages", tag+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	flat, err := read(raw)
	if err != nil {
		t.Fatalf("%s.yaml: %v", tag, err)
	}
	return flat
}

func fields(text string) []string {
	var found []string
	for _, a := range action.FindAllString(text, -1) {
		found = append(found, field.FindAllString(a, -1)...)
	}
	slices.Sort(found)
	return slices.Compact(found)
}

func TestEveryLanguageCarriesEveryString(t *testing.T) {
	english := shipped(t, English.String())
	entries, err := files.ReadDir("messages")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		tag := strings.TrimSuffix(e.Name(), ".yaml")
		if tag == English.String() {
			continue
		}
		t.Run(tag, func(t *testing.T) {
			theirs := shipped(t, tag)
			for _, id := range slices.Sorted(maps.Keys(english)) {
				text, ok := theirs[id]
				if !ok && strings.HasSuffix(id, ".example") {
					continue
				}
				if !ok {
					t.Errorf("%s is missing", id)
					continue
				}
				if want, got := fields(english[id]), fields(text); !slices.Equal(want, got) {
					t.Errorf("%s uses %v, want %v as in English", id, got, want)
				}
			}
			for _, id := range slices.Sorted(maps.Keys(theirs)) {
				if _, ok := english[id]; !ok {
					t.Errorf("%s is not in English", id)
				}
			}
		})
	}
}
