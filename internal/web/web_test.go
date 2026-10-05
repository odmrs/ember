package web

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func catalog(t *testing.T, lang string) map[string]string {
	t.Helper()
	raw, err := fs.ReadFile(Static, "i18n/"+lang+".json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s.json: %v", lang, err)
	}
	return m
}

func TestCatalogsHaveTheSameKeys(t *testing.T) {
	en, pt := catalog(t, "en"), catalog(t, "pt")
	for k := range en {
		if _, ok := pt[k]; !ok {
			t.Errorf("pt missing %q", k)
		}
	}
	for k := range pt {
		if _, ok := en[k]; !ok {
			t.Errorf("en missing %q", k)
		}
	}
}

func TestEveryUsedKeyIsInTheCatalog(t *testing.T) {
	en := catalog(t, "en")
	used := regexp.MustCompile(`data-i18n="([^"]+)"|\bt\("([^"]+)"`)
	walk(t, func(path string) {
		if !(strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".js")) {
			return
		}
		for _, m := range used.FindAllStringSubmatch(read(t, path), -1) {
			key := m[1] + m[2]
			if _, ok := en[key]; !ok {
				t.Errorf("%s uses unknown key %q", path, key)
			}
		}
	})
}

// The dashboard must never reach anything but the local server: no CDN, no
// fonts, no analytics.
func TestNoExternalReferences(t *testing.T) {
	external := regexp.MustCompile(`(?i)(https?:)?//([a-z0-9-]+\.)+[a-z0-9]{2,}`)
	var offenders []string
	walk(t, func(path string) {
		if m := external.FindString(read(t, path)); m != "" {
			offenders = append(offenders, path+": "+m)
		}
	})
	sort.Strings(offenders)
	for _, o := range offenders {
		t.Error("external reference in", o)
	}
}

func walk(t *testing.T, visit func(path string)) {
	t.Helper()
	err := fs.WalkDir(Static, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		visit(path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	src, err := fs.ReadFile(Static, path)
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}
