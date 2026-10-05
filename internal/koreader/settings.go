// Package koreader reads what KOReader keeps about itself on a Kindle.
package koreader

import (
	"regexp"
	"strings"
)

// Where KOReader keeps its files on a Kindle. Its data directory is the
// working directory it runs from, /mnt/us/koreader.
const (
	SettingsPath = "/mnt/us/koreader/settings.reader.lua"
	// MaxSettingsSize bounds how much of the settings file ember reads.
	MaxSettingsSize = 4 << 20
)

// Metadata modes KOReader can store Sidecars in (document_metadata_folder).
const (
	MetadataDoc  = "doc"  // next to the book, the default
	MetadataDir  = "dir"  // mirrored under koreader/docsettings
	MetadataHash = "hash" // under koreader/hashdocsettings, by partial MD5
)

// Settings are the global KOReader settings ember uses. An empty field means
// the setting is absent and KOReader uses its default.
type Settings struct {
	HomeDir      string
	MetadataMode string
	// LastFile is the absolute path of the book KOReader has open, or had open
	// last.
	LastFile string
}

// ParseSettings reads settings.reader.lua. It never fails: whatever it cannot
// read is left empty.
func ParseSettings(src []byte) Settings {
	s := string(src)
	var out Settings
	out.HomeDir, _ = luaString(s, "home_dir")
	out.MetadataMode, _ = luaString(s, "document_metadata_folder")
	out.LastFile, _ = luaString(s, "lastfile")
	return out
}

// luaString finds `["key"] = "value"` in a KOReader settings file and returns
// the decoded value. It is not a Lua parser: KOReader writes its settings as
// one flat table with %q-quoted strings, and the first match wins. A missing
// key, a non-string value or a malformed file all give ok == false.
func luaString(src, key string) (value string, ok bool) {
	re := regexp.MustCompile(`\[\s*"` + regexp.QuoteMeta(key) + `"\s*\]\s*=\s*"`)
	loc := re.FindStringIndex(src)
	if loc == nil {
		return "", false
	}
	var b strings.Builder
	s := src[loc[1]:]
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			return b.String(), true
		case c == '\n':
			return "", false
		case c != '\\':
			b.WriteByte(c)
			continue
		}
		i++
		if i == len(s) {
			return "", false
		}
		switch e := s[i]; e {
		case 'n', '\n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			n, j := 0, i
			for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '9' {
				n = n*10 + int(s[j]-'0')
				j++
			}
			if n > 255 {
				b.WriteString(s[i:j])
			} else {
				b.WriteByte(byte(n))
			}
			i = j - 1
		default:
			b.WriteByte(e)
		}
	}
	return "", false
}
