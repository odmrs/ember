// Package annasarchive searches existing EPUB records without executing site scripts.
package annasarchive

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"bookget/credentials"
	"bookget/safehttp"
	"bookget/source"
)

type Source struct {
	Language        string
	Base            string
	Client          *safehttp.Client
	WikiURL         string
	WikiClient      *safehttp.Client
	Key             string
	LoadKey         func() string
	BrowserFallback bool
	Progress        func(string)
}

func New() *Source {
	return &Source{Base: credentials.Base(), WikiURL: "https://en.wikipedia.org/wiki/Anna%27s_Archive", WikiClient: safehttp.New("en.wikipedia.org"), LoadKey: credentials.Load, BrowserFallback: true}
}

var origin = regexp.MustCompile(`^https://annas-archive\.[a-z]{2,24}/?$`)
var infobox = regexp.MustCompile(`(?is)<table\b[^>]*class=["'][^"']*infobox[^"']*["'][^>]*>.*?</table>`)
var domain = regexp.MustCompile(`https://annas-archive\.[a-z]{2,24}(?:["'/\s<]|$)`)

func discover(data []byte) ([]string, error) {
	box := infobox.Find(data)
	if box == nil {
		return nil, fmt.Errorf("Wikipedia infobox missing; cannot verify Anna's Archive domains")
	}
	seen := map[string]bool{}
	var bases []string
	for _, match := range domain.FindAllString(string(box), -1) {
		base := strings.TrimRight(match, "\"'/ \t\r\n<")
		if origin.MatchString(base) && !seen[base] {
			seen[base] = true
			bases = append(bases, base)
		}
	}
	if len(bases) == 0 {
		return nil, fmt.Errorf("Wikipedia infobox has no recognized Anna's Archive HTTPS domains")
	}
	return bases, nil
}
func (s *Source) origins(ctx context.Context) ([]string, error) {
	if s.Base != "" {
		if s.Client == nil && !origin.MatchString(s.Base) {
			return nil, fmt.Errorf("BOOKGET_ANNA_URL must be an exact Anna's Archive HTTPS origin")
		}
		return []string{strings.TrimSuffix(s.Base, "/")}, nil
	}
	child, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	resp, err := s.WikiClient.Get(child, s.WikiURL)
	if err != nil {
		return nil, fmt.Errorf("domain discovery via Wikipedia: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return discover(data)
}
func (s *Source) Search(ctx context.Context, query string) ([]source.Book, error) {
	bases, err := s.origins(ctx)
	if err != nil {
		return nil, err
	}
	key := s.Key
	if key == "" && s.LoadKey != nil {
		key = s.LoadKey()
	}
	var failures []string
	for _, base := range bases {
		client := s.Client
		if client == nil {
			u, e := url.Parse(base)
			if e != nil {
				return nil, e
			}
			client = safehttp.New(u.Hostname())
		}
		child, cancel := context.WithTimeout(ctx, 8*time.Second)
		if key != "" {
			authed, e := authenticate(child, client, base, key)
			if e != nil {
				cancel()
				failures = append(failures, e.Error())
				continue
			}
			client = authed
		}
		address := base + "/search?" + encodeSearch(query, s.Language)
		resp, e := client.Get(child, address)
		if e != nil {
			cancel()
			failures = append(failures, e.Error())
			continue
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		cancel()
		if e != nil {
			failures = append(failures, e.Error())
			continue
		}
		if dir := os.Getenv("BOOKGET_DEBUG_DIR"); dir != "" && key == "" {
			if e = os.MkdirAll(dir, 0700); e != nil {
				return nil, e
			}
			if e = os.WriteFile(filepath.Join(dir, "anna-search.html"), data, 0600); e != nil {
				return nil, e
			}
		}
		books, e := parse(data, base)
		if e == nil {
			return matchingLanguage(books, query, s.Language), nil
		}
		failures = append(failures, e.Error())
	}
	if s.BrowserFallback && ctx.Err() == nil && len(bases) > 0 {
		if s.Progress != nil {
			s.Progress("O site pediu acesso pelo navegador. Iniciando essa etapa…")
		}
		data, e := browserSearch(ctx, bases[0], query, s.Language, key, s.Progress)
		if e == nil {
			books, parseErr := parse(data, bases[0])
			return matchingLanguage(books, query, s.Language), parseErr
		}
		failures = append(failures, "browser: "+e.Error())
	}
	return nil, fmt.Errorf("Anna's Archive search failed: %s", strings.Join(failures, "; "))
}

func (s *Source) Fetch(ctx context.Context, id, format string) (source.Download, error) {
	if format != "epub" {
		return source.Download{}, fmt.Errorf("existing EPUB files only; conversion is disabled")
	}
	md5 := strings.TrimPrefix(id, "anna:")
	if !md5Path.MatchString("/md5/" + md5) {
		return source.Download{}, fmt.Errorf("invalid Anna's Archive record ID")
	}
	bases, err := s.origins(ctx)
	if err != nil {
		return source.Download{}, err
	}
	key := s.Key
	if key == "" && s.LoadKey != nil {
		key = s.LoadKey()
	}
	return browserFetch(ctx, bases[0], md5, key, s.Progress)
}
func (s *Source) IsAnnasArchive() {}

// SetLanguage restricts searches to the catalog language code.
func (s *Source) SetLanguage(language string) { s.Language = language }

// Authenticated searches include login and server-side full-text search.
func (s *Source) SearchTimeout() time.Duration { return 4 * time.Minute }
