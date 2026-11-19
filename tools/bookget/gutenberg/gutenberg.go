package gutenberg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"bookget/safehttp"
	"bookget/source"
)

type Source struct {
	Base   string
	Client *safehttp.Client
}

func New() *Source {
	return &Source{"https://gutendex.com/books", safehttp.New("gutendex.com", "gutenberg.org", "www.gutenberg.org")}
}

type record struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	Authors []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Copyright *bool             `json:"copyright"`
	Formats   map[string]string `json:"formats"`
}

func book(r record) (source.Book, bool) {
	if r.Copyright == nil || *r.Copyright {
		return source.Book{}, false
	}
	b := source.Book{ID: fmt.Sprintf("gutenberg:%d", r.ID), Title: r.Title, Source: "Project Gutenberg", Formats: map[string]string{}}
	var names []string
	for _, a := range r.Authors {
		names = append(names, a.Name)
	}
	b.Author = strings.Join(names, ", ")
	for mime, u := range r.Formats {
		switch strings.Split(mime, ";")[0] {
		case "application/epub+zip":
			b.Formats["epub"] = u
		case "application/pdf":
			b.Formats["pdf"] = u
		case "application/vnd.amazon.mobi8-ebook":
			b.Formats["azw3"] = u
		}
	}
	return b, len(b.Formats) > 0
}
func (s *Source) Search(ctx context.Context, q string) ([]source.Book, error) {
	address := s.Base + "?copyright=false&search=" + url.QueryEscape(q)
	var books []source.Book
	for page := 0; address != "" && page < 100; page++ {
		resp, err := s.Client.Get(ctx, address)
		if err != nil {
			return books, fmt.Errorf("Gutenberg: %w", err)
		}
		var data struct {
			Next    string   `json:"next"`
			Results []record `json:"results"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&data)
		_ = resp.Body.Close()
		if err != nil {
			return books, err
		}
		for _, r := range data.Results {
			if b, ok := book(r); ok {
				books = append(books, b)
			}
		}
		address = data.Next
	}
	return books, nil
}
func (s *Source) Fetch(ctx context.Context, id, format string) (source.Download, error) {
	num := strings.TrimPrefix(id, "gutenberg:")
	for _, r := range num {
		if r < '0' || r > '9' {
			return source.Download{}, fmt.Errorf("invalid Gutenberg ID")
		}
	}
	if num == "" {
		return source.Download{}, fmt.Errorf("empty Gutenberg ID")
	}
	resp, err := s.Client.Get(ctx, s.Base+"/"+num)
	if err != nil {
		return source.Download{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var r record
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r); err != nil {
		return source.Download{}, err
	}
	b, ok := book(r)
	if !ok {
		return source.Download{}, fmt.Errorf("item is not confirmed public domain or has no supported format")
	}
	return source.Select(b, format, s.Client)
}
func (s *Source) IsGutenberg() {}
