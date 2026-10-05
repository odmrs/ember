package pairing

import "testing"

func TestLuaString(t *testing.T) {
	src := `-- we can read Lua syntax here!
return {
    ["SSH_port"] = "2222",
    ["document_metadata_folder"] = "hash",
    ["home_dir"] = "/mnt/us/documents/Books",
    ["lastfile"] = "/mnt/us/documents/Books/Say \"hi\"\\ \
twice\0491.epub",
    ["nested"] = {
        ["lastfile"] = "ignored, comes later",
    },
    ["number"] = 3,
}
`
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"home_dir":                 {"/mnt/us/documents/Books", true},
		"document_metadata_folder": {"hash", true},
		"lastfile":                 {"/mnt/us/documents/Books/Say \"hi\"\\ \ntwice11.epub", true},
		"number":                   {"", false},
		"missing":                  {"", false},
	}
	for key, c := range cases {
		got, ok := luaString(src, key)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %q, %v; want %q, %v", key, got, ok, c.want, c.ok)
		}
	}
}

func TestLuaStringToleratesGarbage(t *testing.T) {
	for _, src := range []string{"", "return {", `["home_dir"] = "unterminated`, `["home_dir"]="x\`, "\x00\xff"} {
		if got, ok := luaString(src, "home_dir"); ok && got != "" {
			t.Errorf("%q: got %q", src, got)
		}
	}
	if got, _ := luaString(`["home_dir"]="a\q\999b"`, "home_dir"); got != `aq999b` {
		t.Errorf("bad escapes: got %q", got)
	}
}
