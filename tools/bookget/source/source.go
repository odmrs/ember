package source

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"bookget/safehttp"
)

type Book struct {
	ID, Title, Author, Year, Source string
	Publisher                       string `json:",omitempty"`
	Details                         string `json:",omitempty"`
	Formats                         map[string]string
}
type Download struct {
	Book        Book
	Format, URL string
	Client      *safehttp.Client
}
type Source interface {
	Search(context.Context, string) ([]Book, error)
	Fetch(context.Context, string, string) (Download, error)
}

func Select(b Book, format string, c *safehttp.Client) (Download, error) {
	for _, f := range []string{format, "epub", "azw3", "pdf"} {
		if u := b.Formats[f]; u != "" {
			return Download{b, f, u, c}, nil
		}
	}
	return Download{}, fmt.Errorf("no supported ebook format for %s", b.ID)
}
func Normalize(s string) string {
	// Fold common Latin accents without adding a Unicode dependency.
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		for i, group := range []string{"àáâãäå", "èéêë", "ìíîï", "òóôõö", "ùúûü", "ç", "ñ"} {
			if strings.ContainsRune(group, r) {
				r = []rune("aeioucn")[i]
				break
			}
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func SearchAll(ctx context.Context, sources []Source, query string) ([]Book, []error) {
	type result struct {
		books []Book
		err   error
	}
	ch := make(chan result, len(sources))
	var wg sync.WaitGroup
	for _, s := range sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			timeout := 10 * time.Second
			if configured, ok := s.(interface{ SearchTimeout() time.Duration }); ok {
				timeout = configured.SearchTimeout()
			}
			child, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			books, err := s.Search(child, query)
			ch <- result{books, err}
		}(s)
	}
	wg.Wait()
	close(ch)
	unique := map[string]Book{}
	var errs []error
	for r := range ch {
		if r.err != nil {
			errs = append(errs, r.err)
		}
		for _, b := range r.books {
			k := Normalize(b.Title) + "|" + Normalize(b.Author)
			if strings.HasPrefix(b.ID, "anna:") {
				k = b.ID
			}
			old, ok := unique[k]
			if !ok || b.Source == "Standard Ebooks" && old.Source != "Standard Ebooks" {
				unique[k] = b
			}
		}
	}
	books := make([]Book, 0, len(unique))
	for _, b := range unique {
		books = append(books, b)
	}
	sort.Slice(books, func(i, j int) bool {
		if books[i].Title == books[j].Title {
			return books[i].ID < books[j].ID
		}
		return books[i].Title < books[j].Title
	})
	return books, errs
}
