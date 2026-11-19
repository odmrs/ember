package annasarchive

import (
	"fmt"
	"regexp"
	"strings"

	"bookget/source"
	"golang.org/x/net/html"
)

var md5Path = regexp.MustCompile(`^/md5/([a-fA-F0-9]{32})$`)
var year = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)
var epub = regexp.MustCompile(`(?i)(?:^|[·,\s])epub(?:$|[·,\s])`)

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
func descendants(root *html.Node, match func(*html.Node) bool) []*html.Node {
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if match(n) {
			found = append(found, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}
func parse(data []byte, base string) ([]source.Book, error) {
	root, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	books := []source.Book{}
	seen := map[string]bool{}
	links := descendants(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && md5Path.MatchString(attr(n, "href"))
	})
	for _, link := range links {
		match := md5Path.FindStringSubmatch(attr(link, "href"))
		id := strings.ToLower(match[1])
		if seen[id] {
			continue
		}
		card := link
		for i := 0; card.Parent != nil && i < 6; i++ {
			if hasClass(card, "flex") && hasClass(card, "pt-3") {
				break
			}
			card = card.Parent
		}
		// A legacy result may put all fields inside the anchor. Current cards use
		// a separate js-vim-focus title anchor with sibling author/metadata fields.
		title := ""
		if hasClass(link, "js-vim-focus") {
			title = text(link)
		} else {
			headings := descendants(link, func(n *html.Node) bool { return n.Type == html.ElementNode && (n.Data == "h3" || n.Data == "h2") })
			if len(headings) > 0 {
				title = text(headings[0])
				card = link
			}
		}
		if title == "" {
			continue
		}
		meta := ""
		for _, n := range descendants(card, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "font-semibold") && hasClass(n, "text-sm")
		}) {
			value := text(n)
			if epub.MatchString(value) {
				meta = value
				break
			}
		}
		if meta == "" {
			meta = text(card)
		}
		if !epub.MatchString(meta) {
			continue
		}
		name, publisher := "", ""
		icons := descendants(card, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "span" })
		for _, icon := range icons {
			classes := attr(icon, "class")
			if !strings.Contains(classes, "mdi--user-edit") && !strings.Contains(classes, "mdi--domain") && !strings.Contains(classes, "mdi--company") {
				continue
			}
			node := icon
			for node != nil && node != card && node.Data != "a" {
				node = node.Parent
			}
			if node != nil && node.Data == "a" {
				if strings.Contains(classes, "mdi--user-edit") {
					name = text(node)
				} else {
					publisher = text(node)
				}
			}
		}
		if name == "" {
			nodes := descendants(card, func(n *html.Node) bool { return hasClass(n, "italic") })
			if len(nodes) > 0 {
				name = text(nodes[0])
			}
		}
		books = append(books, source.Book{ID: "anna:" + id, Title: title, Author: name, Year: year.FindString(meta), Source: "Anna's Archive", Publisher: publisher, Details: meta, Formats: map[string]string{"epub": base + "/md5/" + id}})
		seen[id] = true
	}
	if len(books) > 0 {
		return books, nil
	}
	lower := strings.ToLower(text(root))
	for _, empty := range []string{"no files found", "no results found", "no matching files"} {
		if strings.Contains(lower, empty) {
			return books, nil
		}
	}
	return nil, fmt.Errorf("response has no recognizable EPUB result rows; cannot conclude the book is absent (site challenge or changed HTML)")
}

func matching(books []source.Book, query string) []source.Book {
	needle := source.Normalize(query)
	if needle == "" {
		return books
	}
	var exact []source.Book
	for _, b := range books {
		if strings.Contains(source.Normalize(b.Title), needle) || strings.Contains(source.Normalize(b.Author), needle) {
			exact = append(exact, b)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return books
}

var languageCode = regexp.MustCompile(`(?i)\[([a-z]{2,3})(?:[-_][a-z]{2,8})?\]`)

func matchingLanguage(books []source.Book, query, language string) []source.Book {
	if language == "" {
		return matching(books, query)
	}
	filtered := make([]source.Book, 0, len(books))
	for _, book := range books {
		code := languageCode.FindStringSubmatch(book.Details)
		if len(code) > 1 && strings.EqualFold(code[1], language) {
			filtered = append(filtered, book)
		}
	}
	return matching(filtered, query)
}
