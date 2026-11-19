package standardebooks

import (
	"context"
	"encoding/xml"
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

func New(user, password string) *Source {
	c := safehttp.New("standardebooks.org", "www.standardebooks.org")
	c.Username = user
	c.Password = password
	return &Source{"https://standardebooks.org/feeds/opds", c}
}

type link struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr"`
}
type entry struct {
	ID      string `xml:"id"`
	Title   string `xml:"title"`
	Authors []struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Published string `xml:"published"`
	Links     []link `xml:"link"`
}

func resolve(base, ref string) string {
	b, e := url.Parse(base)
	if e != nil {
		return ""
	}
	r, e := url.Parse(ref)
	if e != nil {
		return ""
	}
	return b.ResolveReference(r).String()
}
func (s *Source) catalog(ctx context.Context, query, id string) ([]source.Book, error) {
	queue := []string{s.Base}
	seen := map[string]bool{}
	var books []source.Book
	for len(queue) > 0 {
		address := queue[0]
		queue = queue[1:]
		if seen[address] {
			continue
		}
		if len(seen) >= 200 {
			return books, fmt.Errorf("Standard Ebooks catalog page limit exceeded")
		}
		seen[address] = true
		resp, err := s.Client.Get(ctx, address)
		if err != nil {
			return books, fmt.Errorf("Standard Ebooks (OPDS may require SE_OPDS_USER access): %w", err)
		}
		var feed struct {
			Entries []entry `xml:"entry"`
			Links   []link  `xml:"link"`
		}
		err = xml.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&feed)
		_ = resp.Body.Close()
		if err != nil {
			return books, err
		}
		links := feed.Links
		for _, e := range feed.Entries {
			b := source.Book{ID: "standard:" + e.ID, Title: e.Title, Source: "Standard Ebooks", Formats: map[string]string{}}
			var names []string
			for _, a := range e.Authors {
				names = append(names, a.Name)
			}
			b.Author = strings.Join(names, ", ")
			// OPDS publication is the digital edition's publication date.
			if len(e.Published) >= 4 {
				b.Year = e.Published[:4]
			}
			for _, l := range e.Links {
				if strings.HasPrefix(l.Rel, "http://opds-spec.org/acquisition") {
					switch strings.Split(l.Type, ";")[0] {
					case "application/epub+zip":
						b.Formats["epub"] = resolve(address, l.Href)
					case "application/pdf":
						b.Formats["pdf"] = resolve(address, l.Href)
					case "application/vnd.amazon.mobi8-ebook":
						b.Formats["azw3"] = resolve(address, l.Href)
					}
				}
			}
			if len(b.Formats) > 0 {
				match := true
				for _, term := range strings.Fields(query) {
					if !strings.Contains(source.Normalize(b.Title+" "+b.Author), source.Normalize(term)) {
						match = false
					}
				}
				if id != "" {
					match = b.ID == id
				}
				if match {
					books = append(books, b)
					if id != "" {
						return books, nil
					}
				}
			} else {
				links = append(links, e.Links...)
			}
		}
		for _, l := range links {
			if l.Rel == "next" || l.Rel == "subsection" {
				queue = append(queue, resolve(address, l.Href))
			}
		}
	}
	return books, nil
}
func (s *Source) Search(ctx context.Context, q string) ([]source.Book, error) {
	return s.catalog(ctx, q, "")
}
func (s *Source) Fetch(ctx context.Context, id, format string) (source.Download, error) {
	books, err := s.catalog(ctx, "", id)
	if err != nil {
		return source.Download{}, err
	}
	if len(books) == 0 {
		return source.Download{}, fmt.Errorf("Standard Ebooks ID not found")
	}
	return source.Select(books[0], format, s.Client)
}
func (s *Source) IsStandardEbooks() {}
