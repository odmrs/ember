package web

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/odmrs/ember/internal/connector"
	"github.com/odmrs/ember/internal/library"
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

// The dashboard builds these keys from codes the server sends, so the
// catalog check above cannot see them.
func TestEveryServerCodeHasAMessage(t *testing.T) {
	en := catalog(t, "en")
	reasons := []string{
		library.ReasonEmpty, library.ReasonDots, library.ReasonEncoding, library.ReasonReserved, library.ReasonControl,
		library.ReasonTrailing, library.ReasonTooLong, library.ReasonHidden, library.ReasonSidecar,
	}
	for _, r := range reasons {
		if _, ok := en["err.name."+r]; !ok {
			t.Errorf("no message for name reason %q", r)
		}
	}
	for _, a := range []connector.Awake{connector.AwakeOn, connector.AwakeOff, connector.AwakeUnavailable} {
		if _, ok := en["awake."+string(a)]; !ok {
			t.Errorf("no message for keep-awake state %q", a)
		}
	}
	if _, ok := en["state."+string(connector.ReasonDisconnected)]; !ok {
		t.Errorf("no message for wait reason %q", connector.ReasonDisconnected)
	}
	for _, w := range []string{library.WarnSidecarShared, library.WarnSidecarLeft, library.WarnReplacedLeft} {
		if _, ok := en["warn."+w]; !ok {
			t.Errorf("no message for warning %q", w)
		}
	}
}

// jsMaps reads the constant lookup tables of app.js (const NAME = {...};) as
// name -> key -> value.
func jsMaps(t *testing.T) map[string]map[string]string {
	t.Helper()
	block := regexp.MustCompile(`(?s)\nconst ([A-Z_]+) = \{(.*?)\};`)
	pair := regexp.MustCompile(`(\w+): "([^"]*)"`)
	out := map[string]map[string]string{}
	for _, b := range block.FindAllStringSubmatch(read(t, "app.js"), -1) {
		m := map[string]string{}
		for _, p := range pair.FindAllStringSubmatch(b[2], -1) {
			m[p[1]] = p[2]
		}
		out[b[1]] = m
	}
	if len(out["ERRORS"]) == 0 {
		t.Fatal("no ERRORS table found in app.js")
	}
	return out
}

// Every code the server can answer with, and every code the dashboard makes
// up itself, reads as a catalog message instead of the server's raw words.
func TestEveryErrorCodeHasAMessage(t *testing.T) {
	en := catalog(t, "en")
	tables := jsMaps(t)
	errs := tables["ERRORS"]

	codes := map[string]string{}
	sources, err := filepath.Glob("../server/*.go")
	if err != nil || len(sources) == 0 {
		t.Fatalf("server sources: %v", err)
	}
	written := regexp.MustCompile(`writeErrorCode\([^,]+,[^,]+,\s*"([a-z_]+)"|"error":\s*"([a-z_]+)"`)
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range written.FindAllStringSubmatch(string(src), -1) {
			codes[m[1]+m[2]] = path
		}
	}
	if len(codes) < 10 {
		t.Fatalf("found only %d server codes; the pattern no longer matches writeError", len(codes))
	}
	made := regexp.MustCompile(`new APIError\("([a-z_]+)"`)
	for _, m := range made.FindAllStringSubmatch(read(t, "app.js"), -1) {
		codes[m[1]] = "app.js"
	}
	for _, c := range tables["HEAD_ERRORS"] {
		codes[c] = "app.js HEAD_ERRORS"
	}
	// Reported in the pairing state rather than as an HTTP error.
	codes["key_wait_timeout"] = "pairing state"

	for code, from := range codes {
		if code == "invalid_name" {
			continue // its reason picks the message, checked above
		}
		key, ok := errs[code]
		if !ok {
			t.Errorf("code %q (%s) is not in ERRORS", code, from)
			continue
		}
		if _, ok := en[key]; !ok {
			t.Errorf("code %q maps to %q, which is not in the catalog", code, key)
		}
	}
	for _, key := range []string{"err.internal", "err.name.invalid"} {
		if _, ok := en[key]; !ok {
			t.Errorf("fallback message %q is missing", key)
		}
	}
}

// Message keys kept in lookup tables are not t("...") calls, so the check on
// used keys cannot see them.
func TestEveryTableKeyIsInTheCatalog(t *testing.T) {
	en := catalog(t, "en")
	for name, table := range jsMaps(t) {
		for k, v := range table {
			if !strings.Contains(v, ".") {
				continue
			}
			if _, ok := en[v]; !ok {
				t.Errorf("%s[%s] = %q is not in the catalog", name, k, v)
			}
		}
	}
}

func TestRetiredKeysAreGone(t *testing.T) {
	for _, lang := range []string{"en", "pt"} {
		c := catalog(t, lang)
		// There is no help screen, and names ending in .part are allowed.
		for _, k := range []string{"help", "err.name.part"} {
			if _, ok := c[k]; ok {
				t.Errorf("%s.json still has %q", lang, k)
			}
		}
	}
	if strings.Contains(read(t, "index.html"), `data-i18n="help"`) {
		t.Error("index.html still shows the help button")
	}
}

// The candidate survey reads KOReader's default Library root; the dashboard
// spells it in one place.
func TestDefaultRootIsNamedOnce(t *testing.T) {
	if n := strings.Count(read(t, "app.js"), "/mnt/us/documents"); n != 1 {
		t.Errorf("app.js spells the default library root %d times, want 1", n)
	}
}

// The theme must be on <html> before the page paints, and the CSP allows no
// inline script or style, so a blocking script file in <head> sets it.
func TestThemeIsSetBeforeFirstPaint(t *testing.T) {
	html := read(t, "index.html")
	head, _, ok := strings.Cut(html, "</head>")
	if !ok {
		t.Fatal("index.html has no </head>")
	}
	if !strings.Contains(head, `<script src="theme.js"></script>`) {
		t.Error(`<head> must load theme.js without defer or async`)
	}
	if strings.Index(head, "theme.js") > strings.Index(head, "app.js") {
		t.Error("theme.js must load before app.js")
	}
	read(t, "theme.js")
	en := catalog(t, "en")
	for _, mode := range []string{"auto", "light", "dark"} {
		for _, key := range []string{"theme." + mode, "theme." + mode + "Hint"} {
			if _, ok := en[key]; !ok {
				t.Errorf("no message %q for the theme button", key)
			}
		}
	}
	if regexp.MustCompile(`<script(\s[^>]*)?>[^<]`).MatchString(html) || !regexp.MustCompile(`<script[^>]*\ssrc=`).MatchString(html) {
		t.Error("index.html has an inline script")
	}
	if regexp.MustCompile(`(?i)<style|\sstyle=`).MatchString(html) {
		t.Error("index.html has inline style")
	}
}

// Colours live only in the theme tokens, and both themes define every token.
func TestColoursAreThemeTokens(t *testing.T) {
	css := read(t, "app.css")
	colour := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgba?\(`)
	token := regexp.MustCompile(`^\s*--[\w-]+:`)
	for i, line := range strings.Split(css, "\n") {
		if colour.MatchString(line) && !token.MatchString(line) {
			t.Errorf("app.css:%d: colour outside a token: %s", i+1, strings.TrimSpace(line))
		}
	}
	blocks := map[string]map[string]bool{}
	block := regexp.MustCompile(`(?s)(:root(?:\[data-theme="light"\])?) \{(.*?)\}`)
	name := regexp.MustCompile(`--([\w-]+):`)
	for _, b := range block.FindAllStringSubmatch(css, -1) {
		set := map[string]bool{}
		for _, n := range name.FindAllStringSubmatch(b[2], -1) {
			set[n[1]] = true
		}
		blocks[b[1]] = set
	}
	dark, light := blocks[":root"], blocks[`:root[data-theme="light"]`]
	if len(dark) == 0 || len(light) == 0 {
		t.Fatalf("theme blocks not found: %v", blocks)
	}
	for n := range dark {
		if n != "mono" && !light[n] {
			t.Errorf("light theme lacks --%s", n)
		}
	}
	for n := range light {
		if !dark[n] {
			t.Errorf("light theme defines --%s, unknown to the dark one", n)
		}
	}
}
