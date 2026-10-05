package names

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Metadata is what a Book says about itself, as read from inside the file.
type Metadata struct {
	Title   string
	Authors []string
}

// Where a proposed name came from.
const (
	SourceMetadata = "metadata" // the Book's own title and authors
	SourcePattern  = "pattern"  // a download site's naming convention
	SourceGeneric  = "generic"  // noise stripped from the file name
)

// Clean proposes the name "Title - Author.ext" for the Book named fileName.
// The title and authors come from meta when it holds a plausible title, else
// from the file name itself. A name that already reads clean is left alone,
// and so is any name the cleanup cannot improve: changed is then false and
// proposed is fileName. Clean never returns a name Valid refuses.
func Clean(fileName string, meta Metadata) (proposed, source string, changed bool) {
	stem, ext := splitExt(fileName)
	parsed := parse(stem)
	fromName := assemble(parsed.title, parsed.authors, ext)
	if !parsed.matched && fromName == fileName {
		return fileName, "", false
	}

	name, src := fromName, SourceGeneric
	if parsed.matched {
		src = SourcePattern
	}
	if title := metaTitle(meta.Title); title != "" {
		authors := metaAuthors(meta.Authors)
		if len(authors) == 0 && parsed.matched {
			authors = parsed.authors
		}
		name, src = assemble(title, authors, ext), SourceMetadata
	} else if parsed.title == "" {
		return fileName, "", false
	}
	if name == "" || name == fileName || Valid(name) != nil {
		return fileName, "", false
	}
	return name, src, true
}

// compound are the extensions KOReader reads inside a zip.
var compound = []string{".fb2.zip", ".txt.zip", ".html.zip", ".htm.zip", ".rtf.zip", ".md.zip"}

var plainExt = regexp.MustCompile(`^\.[A-Za-z0-9]{1,5}$`)

// splitExt splits off the extension, lower-cased with its dot. A name whose
// last dot does not start a plausible extension has none.
func splitExt(name string) (stem, ext string) {
	lower := strings.ToLower(name)
	for _, c := range compound {
		if strings.HasSuffix(lower, c) && len(name) > len(c) {
			return name[:len(name)-len(c)], c
		}
	}
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || !plainExt.MatchString(name[i:]) {
		return name, ""
	}
	return name[:i], lower[i:]
}

// parsed is a file name taken apart.
type parsed struct {
	title   string
	authors []string
	// matched says a download site's convention was recognised.
	matched bool
}

var (
	innerExt    = regexp.MustCompile(`(?i)\.(epub|pdf|mobi|azw3?|fb2|kepub|djvu|cbz|cbr)$`)
	dupCounter  = regexp.MustCompile(`\s+\(\d{1,2}\)$`)
	dottedAnna  = regexp.MustCompile(`\.--\.`)
	annaShort   = regexp.MustCompile(`(?i)^annas-arch-[0-9a-f]+$`)
	hexFragment = regexp.MustCompile(`(?i)^[0-9a-f]{6,40}$`)
	hexHash     = regexp.MustCompile(`(?i)^[0-9a-f]{32}$`)

	zlibTag      = regexp.MustCompile(`(?i)\s*\([^()]*\bz-?lib(?:rary)?\b[^()]*\)$|\s*\bz[-_ ]?library[._ ]sk\b.*$|\s*\bz-lib\.org\b.*$`)
	zlibByAuthor = regexp.MustCompile(`^(.+?)\s+by\s+([^()]+)$`)
	zlibPubTail  = regexp.MustCompile(`\s*[—–]_?[^()]*\d{10,13}$`)
	libgenTag    = regexp.MustCompile(`(?i)(?:\s+-\s+|\s+|\.)libgen\.[a-z]{2,3}$|\s*\(\s*libgen\.[a-z]{2,3}\s*\)$`)
	curlyGroup   = regexp.MustCompile(`\{([^{}]*)\}`)
	leadSeries   = regexp.MustCompile(`^(?:\s*\[[^\[\]]*\])+\s*`)
	oceanTag     = regexp.MustCompile(`(?i)^_*oceanofpdf\.com_+`)
	pdfDriveTag  = regexp.MustCompile(`(?i)\s*\(\s*pdf\s?drive(?:\.com)?\s*\)\s*`)
	epubLibreRev = regexp.MustCompile(`(?i)\s*\((?:r\d+(?:\.\d+)*)(?:\s*-\s*EPL)?\)\s*$|\s*\[EPL-FS\].*$`)
	standardEbk  = regexp.MustCompile(`^([a-z0-9]+(?:-[a-z0-9]+)+)_([a-z0-9]+(?:-[a-z0-9]+)*)(?:_[a-z0-9-]+)*$`)
)

// parse takes the title and authors out of a file name's stem, following a
// download site's convention when it recognises one.
func parse(stem string) parsed {
	s := strings.TrimSpace(innerExt.ReplaceAllString(stem, ""))
	s = strings.TrimSpace(dupCounter.ReplaceAllString(s, ""))
	if dottedAnna.MatchString(s) {
		s = strings.ReplaceAll(s, ".", " ")
	}
	if annaShort.MatchString(s) {
		return parsed{matched: true}
	}
	if p, ok := annasArchive(s); ok {
		return p
	}
	if m := standardEbk.FindStringSubmatch(s); m != nil {
		// Standard Ebooks names its files author_title, in lower-case slugs.
		return finish(strings.ReplaceAll(m[2], "-", " "), strings.ReplaceAll(m[1], "-", " "), true)
	}
	if oceanTag.MatchString(s) {
		s = strings.ReplaceAll(oceanTag.ReplaceAllString(s, ""), "_", " ")
		if title, author, ok := cutLast(spaces(s), " - "); ok {
			return finish(title, author, true)
		}
		return finish(s, "", true)
	}
	if !strings.Contains(s, " ") {
		// A name with no spaces at all uses "_" for them.
		s = underscores.ReplaceAllString(s, " ")
	}
	if epubLibreRev.MatchString(s) {
		// ePubLibre names its files "Surname, Name - [Series NN] Title (r1.0)".
		s = leadSeries.ReplaceAllString(epubLibreRev.ReplaceAllString(s, ""), "")
		if author, title, ok := strings.Cut(s, " - "); ok && personList(author) {
			return finish(leadSeries.ReplaceAllString(title, ""), author, true)
		}
		return finish(s, "", true)
	}
	if libgenTag.MatchString(s) {
		s = libgenTag.ReplaceAllString(s, "")
		// One Library Genesis variant names the author in braces.
		for _, m := range curlyGroup.FindAllStringSubmatch(s, -1) {
			if personList(m[1]) {
				title := strings.TrimSpace(s[:strings.Index(s, "{")])
				return finish(title, m[1], true)
			}
		}
		s = curlyGroup.ReplaceAllString(s, " ")
		// Library Genesis names its files "Author - Title (Year, Publisher)".
		s = leadSeries.ReplaceAllString(s, "")
		if author, title, ok := strings.Cut(s, " - "); ok && personList(author) {
			return finish(title, author, true)
		}
		return finish(s, "", true)
	}
	if zlibTag.MatchString(s) {
		s = zlibPubTail.ReplaceAllString(zlibTag.ReplaceAllString(s, ""), "")
		// Z-Library names its files "Title (Author)", or "Title by Author".
		if title, author, ok := lastGroup(s); ok && personList(author) && !noise(author) {
			return finish(title, author, true)
		}
		if m := zlibByAuthor.FindStringSubmatch(s); m != nil && personList(m[2]) {
			return finish(m[1], m[2], true)
		}
		return finish(s, "", true)
	}
	if pdfDriveTag.MatchString(s) {
		return finish(pdfDriveTag.ReplaceAllString(s, " "), "", true)
	}
	return finish(s, "", false)
}

// annasArchive reads Anna's Archive names: "Title -- Author -- Edition,
// Year -- Publisher -- isbn13 ... -- md5 -- Anna's Archive", fields left out
// when unknown and the whole cut short when too long.
func annasArchive(stem string) (parsed, bool) {
	fields := strings.Split(stem, " -- ")
	if len(fields) < 2 {
		return parsed{}, false
	}
	last := strings.TrimSpace(fields[len(fields)-1])
	tail := isAnnaPrefix(last) || hexFragment.MatchString(last)
	if len(fields) < 3 && !tail {
		return parsed{}, false
	}
	author := ""
	if len(fields) >= 3 || !tail {
		if f := strings.TrimSpace(fields[1]); !annaMeta(f) {
			author = f
		}
	}
	return finish(annaText(fields[0]), annaText(author), true), true
}

// isAnnaPrefix reports whether s is "Anna's Archive" or the start of it,
// as left by a name cut short.
func isAnnaPrefix(s string) bool {
	s = strings.ToLower(strings.NewReplacer("’", "'", "anna s", "anna's").Replace(s))
	return len(s) >= 3 && strings.HasPrefix("anna's archive", s)
}

var (
	hasDigit  = regexp.MustCompile(`\d`)
	publisher = regexp.MustCompile(`(?i)\b(press|publish\w*|publications?|books|editora|editorial|ediciones|edições|edicoes|verlag|éditions|ltd|inc|llc|gmbh|classics|penguin|harper\w*|wiley|springer|elsevier|o'reilly|packt|apress|manning|pearson|mcgraw|routledge|vintage|scholastic|doubleday|knopf|bantam|lelivros|epublibre)\b`)
)

// annaMeta reports whether an Anna's Archive field is something other than
// an author: an edition, a year, a publisher, an ISBN or the hash.
func annaMeta(f string) bool {
	return f == "" || strings.EqualFold(f, "none") || hasDigit.MatchString(f) || isAnnaPrefix(f) ||
		hexFragment.MatchString(f) || publisher.MatchString(f) || editionWord.MatchString(f)
}

var abbrevUnderscore = regexp.MustCompile(`(^|[\s,;])(\p{Lu}|Mr|Mrs|Ms|Dr|St|Jr|Sr|Vol|No|Sra|Dra)_(\s|,|;|$)`)

// annaText undoes the escaping of Anna's Archive and Library Genesis, which
// write "_" for a dot after an initial or an abbreviation, and for a colon.
func annaText(s string) string {
	for range 2 {
		s = abbrevUnderscore.ReplaceAllString(s, "$1$2.$3")
	}
	s = strings.ReplaceAll(s, " _ ", ": ")
	s = strings.ReplaceAll(s, "_ ", ": ")
	return strings.ReplaceAll(s, "_", " ")
}

// cutLast splits s around the last sep.
func cutLast(s, sep string) (before, after string, ok bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// lastGroup splits "Title (Group)" into its title and the group's content.
func lastGroup(s string) (before, inside string, ok bool) {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, ")") {
		return s, "", false
	}
	depth := 0
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[:i]), s[i+1 : len(s)-1], i > 0
			}
		}
	}
	return s, "", false
}

var (
	personChars = regexp.MustCompile(`^[\p{L}\p{M}.,'’\-&; ]+$`)
	titleWords  = regexp.MustCompile(`(?i)\b(of|the|in|on|for|to|with|from|at|by|an)\b`)
)

// personList reports whether s could be one or more people's names: no
// digits or brackets, and none of the little words titles are made of.
func personList(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && personChars.MatchString(s) && !titleWords.MatchString(s) && len(strings.Fields(s)) <= 12
}

// finish cleans a title and an author field taken from a file name. With no
// author field it cleans each " - " part on its own, keeping their order: a
// bare "A - B" may be title then author or author then title.
func finish(title, author string, matched bool) parsed {
	if author == "" {
		var parts []string
		for _, part := range strings.Split(generic(title), " - ") {
			if part = fixCase(cleanTitle(part)); part != "" && !noisePart(part) {
				parts = append(parts, part)
			}
		}
		return parsed{title: strings.Join(parts, " - "), matched: matched}
	}
	var authors []string
	for _, a := range splitAuthors(generic(author)) {
		authors = append(authors, fixCase(a))
	}
	t := fixCase(cleanTitle(generic(title)))
	if len(authors) > 0 {
		// "The Complete Stories by Guy de Maupassant" says the author twice.
		if i := strings.Index(strings.ToLower(t), " by "+strings.ToLower(authors[0])); i > 0 {
			t = tidy(t[:i])
		}
	}
	return parsed{title: t, authors: authors, matched: matched}
}

var (
	underscores  = regexp.MustCompile(`_+`)
	dottedWords  = regexp.MustCompile(`\p{L}{2,}\.\p{L}{2,}`)
	slugToken    = regexp.MustCompile(`^[\p{L}\p{N}]+(?:-[\p{L}\p{N}]+){3,}$`)
	slugYear     = regexp.MustCompile(`[\s-]+(?:1[5-9]|20)\d{2}$`)
	md5Token     = regexp.MustCompile(`(?i)\b[0-9a-f]{32}\b|\b[0-9a-f]{40}\b`)
	isbnToken    = regexp.MustCompile(`(?i)\bisbn(?:-?1[03])?[\s:_-]*(?:97[89][\d-]{10,14}|[\d-]{9,12}[\dx])\b|\b97[89]-?\d{1,5}-?\d{1,7}-?\d{1,7}-?\d\b|\b97[89]\d{10}\b`)
	asinTail     = regexp.MustCompile(`[_\s]B0[0-9A-Z]{8}$`)
	annaWords    = regexp.MustCompile(`(?i)[…\s.-]*anna['’ ]?s archive`)
	urlToken     = regexp.MustCompile(`(?i)\b(?:https?://)?www\.[\w.-]+\.[a-z]{2,}\S*|\b[\w-]+\.(?:com|org|net|info|sk|rs|li|lc|gs|is|me|to|cc|ws)\b`)
	symbols      = strings.NewReplacer("©", " ", "®", " ", "™", " ")
	trailingJunk = regexp.MustCompile(`(?i)[\s\-_,.]*\b(?:retail|e-?books?|epub3?|kepub|pdf|mobi|azw3?|kindle|scan|ocr|lcp|v\d+(?:\.\d+)*|r\d+(?:\.\d+)*|copy|cópia|copia)\s*$|\s*(?:\.\.\.|…)+\s*$`)
	groups       = regexp.MustCompile(`\s*[(\[{]([^()\[\]{}]*)[)\]}]`)
	dashes       = regexp.MustCompile(`\s*(?:--+|[–—])\s*|\s+-\s*|\s*-\s+`)
	space        = regexp.MustCompile(`\s+`)
	edgePunct    = regexp.MustCompile(`^[\s\-_,;.:…]+|[\s\-_,;:…]+$`)
)

// generic strips what no title needs from a file name: separators used for
// spaces, site tags, hashes, ISBNs, bracketed notes and the counters
// browsers add to repeated downloads.
func generic(s string) string {
	s = dupCounter.ReplaceAllString(strings.TrimSpace(s), "")
	slug := false
	if !strings.ContainsRune(strings.TrimSpace(s), ' ') {
		switch {
		case strings.Contains(s, "_"):
			s, slug = underscores.ReplaceAllString(s, " "), true
		case dottedWords.MatchString(s):
			s, slug = strings.ReplaceAll(s, ".", " "), true
		}
	}
	words := strings.Fields(s)
	for i, w := range words {
		if slugToken.MatchString(strings.Trim(w, "-")) {
			words[i], slug = strings.ReplaceAll(w, "-", " "), true
		}
	}
	s = strings.Join(words, " ")
	if slug {
		s = slugYear.ReplaceAllString(s, "")
	}
	s = strings.ReplaceAll(s, "__ ", ": ")
	s = strings.ReplaceAll(s, "_ ", ": ")
	s = underscores.ReplaceAllString(s, " ")
	s = asinTail.ReplaceAllString(s, "")
	s = md5Token.ReplaceAllString(s, " ")
	s = isbnToken.ReplaceAllString(s, " ")
	s = annaWords.ReplaceAllString(s, " ")
	s = symbols.Replace(s)
	for {
		next := groups.ReplaceAllStringFunc(s, func(g string) string {
			if noise(groups.FindStringSubmatch(g)[1]) {
				return " "
			}
			return g
		})
		if next == s {
			break
		}
		s = next
	}
	s = urlToken.ReplaceAllString(s, " ")
	for {
		next := strings.TrimRight(dupCounter.ReplaceAllString(trailingJunk.ReplaceAllString(s, ""), ""), " ")
		if next == s {
			break
		}
		s = next
	}
	return tidy(s)
}

// tidy collapses spaces and dashes and trims separators off the ends.
func tidy(s string) string {
	s = space.ReplaceAllString(s, " ")
	s = dashes.ReplaceAllString(s, " - ")
	for {
		next := strings.ReplaceAll(s, " - - ", " - ")
		if next == s {
			break
		}
		s = next
	}
	return edgePunct.ReplaceAllString(strings.TrimSpace(s), "")
}

var (
	yearWord    = regexp.MustCompile(`\b(1[5-9]|20)\d{2}\b`)
	yearOnly    = regexp.MustCompile(`^(1[5-9]|20)\d{2}(\s*[;,]\s*(1[5-9]|20)\d{2})*$`)
	editionWord = regexp.MustCompile(`(?i)(\bedition\b|\bed\.|\bedição\b|\bedicao\b|\bedición\b|\bedicion\b|\bédition\b|\bauflage\b|\bedizione\b)`)
	siteWord    = regexp.MustCompile(`(?i)(z-?lib|1lib|libgen|pdf\s?drive|oceanofpdf|anna'?s archive|epub\s?libre|lectulandia|le\s?livros|www\.|\.(com|org|net|sk|rs|li|lc|gs|is|me|to)\b)`)
	formatWords = regexp.MustCompile(`(?i)^(e-?pub3?|pdf|mobi|azw3?|kindle|kindle edition|e-?book|retail|scan|ocr|html|txt|fb2|true\s?pdf|converted|calibre|[vr]\s?\d+(\.\d+)*|e-?pub\s+[vr]?\d+(\.\d+)*|unabridged|abridged|illustrated|annotated|ilustrado|anotado|ilustrada|anotada|completo|completa|unknown|new)$`)
	langWords   = regexp.MustCompile(`(?i)^(english|spanish|español|espanol|castellano|portuguese|português|portugues|pt-?br|pt|es|en|french|français|german|deutsch|italian|italiano)(\s+(version|versión|versão|edition))?$`)
	isbnOnly    = regexp.MustCompile(`(?i)^(isbn[\s:-]*)?[\d-]{9,17}x?$`)
	idToken     = regexp.MustCompile(`^[\w-]*\d{5,}[\w-]*$|^[\w-]+\s[\w-]*\d{5,}[\w-]*$`)
)

// noisePart reports whether a whole " - " part of a name is a note about
// the file rather than a title or an author: a year, an edition, a site, a
// format, a language, an ISBN, an id or a hash.
func noisePart(part string) bool {
	s := strings.TrimSpace(part)
	switch {
	case s == "", yearOnly.MatchString(s):
		return true
	case isbnOnly.MatchString(s), hexHash.MatchString(s), idToken.MatchString(s):
		return true
	case editionWord.MatchString(s), siteWord.MatchString(s), formatWords.MatchString(s), langWords.MatchString(s):
		return true
	}
	return false
}

// noise reports whether the text of a bracketed group is a note: what
// noisePart refuses, and also a year with a place or a publisher, as in
// "(2016, Benvirá)", or an imprint, as in "(Penguin Classics)". A whole
// part named like an imprint may still be a title: "Books of Blood".
func noise(inner string) bool {
	n := len(strings.Fields(inner))
	return noisePart(inner) || yearWord.MatchString(inner) && n <= 6 || publisher.MatchString(inner) && n <= 4
}

var (
	ordinal      = `(?:\d+(?:st|nd|rd|th|ª|º|a|o)?|first|second|third|fourth|fifth|sixth|seventh|eighth|ninth|tenth|new|revised|updated|expanded|anniversary|international|global|special|collector'?s|deluxe|definitive|complete|illustrated|annotated|kindle|student|nova|revista|ampliada|atualizada|nueva|revisada|primeira|segunda|terceira|primera|tercera|spanish|english|portuguese|greek|french|german|italian)`
	editionCut   = regexp.MustCompile(`(?i)[\s,;.:(-]*\b(?:` + ordinal + `\s+)+(?:edition|ed\.|edição|edicao|edición|edicion)\b.*$`)
	subtitleDash = regexp.MustCompile(`(?i)\s+-\s+(?:a novel|a memoir|um romance|uma novela|romance|novela|una novela)\s*$`)
	credential   = regexp.MustCompile(`(?i),?\s+(?:ph\.?\s?d\.?|m\.?d\.?|dphil)$`)
)

// cleanTitle drops a subtitle after a colon, or after a dash when it is
// plainly one, and an edition with whatever follows it.
func cleanTitle(s string) string {
	s = tidy(s)
	for _, colon := range []string{":", "："} {
		if head, _, ok := strings.Cut(s, colon); ok && strings.TrimSpace(head) != "" {
			s = head
		}
	}
	if cut := editionCut.ReplaceAllString(s, ""); strings.TrimSpace(cut) != "" {
		s = cut
	}
	s = subtitleDash.ReplaceAllString(s, "")
	return tidy(credential.ReplaceAllString(tidy(s), ""))
}

// metaTitle cleans a title read from inside a Book, keeping its case, or
// returns "" when it is not plausibly the book's title.
func metaTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if !plausibleTitle(s) {
		return ""
	}
	s = groups.ReplaceAllStringFunc(s, func(g string) string {
		if noise(groups.FindStringSubmatch(g)[1]) {
			return " "
		}
		return g
	})
	s = cleanTitle(symbols.Replace(s))
	if !plausibleTitle(s) {
		return ""
	}
	return s
}

var junkTitle = regexp.MustCompile(`(?i)^(untitled|untitled document|document\d*|title|unknown|none|no title|sem título|sin título|book|ebook|e-book|cover|capa|portada)$|\.(docx?|pdf|epub|mobi|azw3?|indd|tex|dvi|rtf|odt|html?|txt|qxd|ps)$|^microsoft (word|powerpoint) - `)

// plausibleTitle refuses titles that tools fill in on their own.
func plausibleTitle(s string) bool {
	if s == "" || junkTitle.MatchString(s) || hexFragment.MatchString(s) || isbnOnly.MatchString(s) {
		return false
	}
	return strings.ContainsFunc(s, unicode.IsLetter) || strings.ContainsFunc(s, unicode.IsDigit) && utf8.RuneCountInString(s) <= 6
}

// metaAuthors cleans the authors read from inside a Book.
func metaAuthors(list []string) []string {
	return splitAuthors(strings.Join(list, "; "))
}

var (
	roleClause  = regexp.MustCompile(`(?i)^(?:(?:drawings|illustrations?|illustrated|translated|translation|edited|foreword|introduction|preface|afterword|notes|selected|compiled|adapted|retold|narrated|read|cover)\s+(?:by\s+)?|(?:tradução|traducción|traduccion|traduzido|ilustrações|ilustraciones|ilustrado|prefácio|prólogo|prologo|introdução|introducción|organização|organizado|editado|edição|adaptação|adaptación|notas)\s+(?:de\s+|por\s+)?|(?:trad|ed|eds|org|orgs|coord|il|illus)\.\s+)`)
	roleNote    = regexp.MustCompile(`(?i)\s*[(\[](?:[^()\[\]]*\b(?:ed|eds|editor|editors|editora|org|orgs|organizador|organizadora|coord|coordenador|coordinador|compiler|author|autor|autora)\b\.?[^()\[\]]*)[)\]]`)
	helperNote  = regexp.MustCompile(`(?i)[(\[][^()\[\]]*\b(?:trad|tradutor|tradutora|traductor|traductora|translator|translated|illustrator|illustrated|ilustrador|ilustradora|introduction|foreword|preface|prefácio|prólogo|afterword|narrator|cover)\b[^()\[\]]*[)\]]`)
	roleSuffix  = regexp.MustCompile(`(?i),?\s+(?:\(?eds?\.?\)?|editors?|\(?orgs?\.?\)?|\(?coord\.?\)?)$`)
	andWord     = regexp.MustCompile(`(?i)\s+(?:and|&)\s+|\s*&\s*`)
	initialOnly = regexp.MustCompile(`^(?:\p{Lu}\.?\s*)+$`)
	nobody      = regexp.MustCompile(`(?i)^(unknown|desconhecido|desconocido|autor desconhecido|autor desconocido|n/?a|none|various|vários|varios|author|autor|autora|editor|translator|administrator|admin|user|owner)$`)
	eastAsian   = regexp.MustCompile(`\p{Han}|\p{Hiragana}|\p{Katakana}|\p{Hangul}`)
)

// splitAuthors turns an author field into names as they are read: roles and
// notes dropped, "Last, First" turned round, duplicates removed.
func splitAuthors(field string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(a string) {
		a = strings.TrimSpace(a)
		if roleClause.MatchString(a) || helperNote.MatchString(a) {
			return
		}
		a = tidy(credential.ReplaceAllString(roleSuffix.ReplaceAllString(roleNote.ReplaceAllString(a, ""), ""), ""))
		a = strings.Trim(a, " ,;")
		if a == "" || nobody.MatchString(a) || hasDigit.MatchString(a) || !strings.ContainsFunc(a, unicode.IsLetter) {
			return
		}
		if k := strings.ToLower(a); !seen[k] {
			seen[k] = true
			out = append(out, a)
		}
	}
	for _, part := range strings.FieldsFunc(field, func(r rune) bool { return r == ';' || r == '；' }) {
		part = strings.TrimSpace(roleNote.ReplaceAllString(part, ""))
		if part == "" || roleClause.MatchString(part) {
			continue
		}
		// "Weis, Margaret & Hickman, Tracy": two people, each surname first.
		joined := andWord.Split(part, -1)
		if len(joined) > 1 && allContain(joined, ",") {
			for _, j := range joined {
				for _, a := range commaNames(j) {
					add(a)
				}
			}
			continue
		}
		for _, a := range commaNames(part) {
			for _, b := range andWord.Split(a, -1) {
				add(b)
			}
		}
	}
	return out
}

func allContain(list []string, sub string) bool {
	for _, s := range list {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// commaNames reads the commas in one author entry: "Orwell, George" is one
// person written surname first, "Ann Smith, Bob Jones" two people.
func commaNames(part string) []string {
	var pieces []string
	for _, p := range strings.Split(part, ",") {
		if p = strings.TrimSpace(p); p != "" && !nobody.MatchString(p) {
			pieces = append(pieces, p)
		}
	}
	if len(pieces) == 2 && inverted(pieces[0], pieces[1]) {
		if eastAsian.MatchString(pieces[0] + pieces[1]) {
			// East Asian names put the family name first anyway.
			return []string{pieces[0] + " " + pieces[1]}
		}
		return []string{pieces[1] + " " + pieces[0]}
	}
	return pieces
}

// inverted reports whether "last, first" is one name written surname first.
func inverted(last, first string) bool {
	if andWord.MatchString(last) || andWord.MatchString(first) || strings.ContainsAny(last+first, "([") {
		return false
	}
	lw, fw := strings.Fields(last), strings.Fields(first)
	if len(lw) == 1 {
		return len(fw) <= 3
	}
	if len(fw) == 1 || initialOnly.MatchString(first) {
		return len(lw) <= 3
	}
	// "Le Guin, Ursula K.": given names then initials.
	return len(fw) == 2 && initialOnly.MatchString(fw[1]) && len(lw) <= 3
}

// fixCase turns text from a file name that is all upper or all lower case
// into title case. Mixed case is someone's choice and stays.
func fixCase(s string) string {
	var upper, lower int
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			upper++
		case unicode.IsLower(r):
			lower++
		}
	}
	if upper+lower < 4 || upper > 0 && lower > 0 {
		return s
	}
	return titleCase(s, lower == 0)
}

var smallWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true, "of": true, "in": true, "on": true,
	"at": true, "to": true, "for": true, "by": true, "with": true, "from": true, "as": true, "nor": true,
	"de": true, "da": true, "do": true, "das": true, "dos": true, "e": true, "o": true, "os": true,
	"em": true, "no": true, "na": true, "nos": true, "nas": true, "um": true, "uma": true, "por": true,
	"para": true, "com": true, "del": true, "la": true, "las": true, "el": true, "los": true, "y": true,
	"en": true, "un": true, "una": true, "con": true, "al": true, "se": true, "lo": true,
}

var (
	roman     = regexp.MustCompile(`^(?i:[ivxlcdm]{2,})$`)
	romanForm = regexp.MustCompile(`^m{0,3}(cm|cd|d?c{0,3})(xc|xl|l?x{0,3})(ix|iv|v?i{0,3})$`)
)

// titleCase capitalises each word except short joining words inside the
// title. Roman numerals keep their capitals when the text had them.
func titleCase(s string, wasUpper bool) string {
	words := strings.Split(strings.ToLower(s), " ")
	for i, w := range words {
		bare := strings.Trim(w, ",.;:!?'\"()[]")
		switch {
		case w == "":
		case wasUpper && roman.MatchString(bare) && romanForm.MatchString(bare):
			words[i] = strings.ToUpper(w)
		case i > 0 && smallWords[bare] && words[i-1] != "-":
		default:
			words[i] = capitalize(w)
		}
	}
	return strings.Join(words, " ")
}

// capitalize upper-cases the first letter of w and of each part after a
// hyphen or a dot, so "spider-man" and "j.r.r." come out right.
func capitalize(w string) string {
	rs := []rune(w)
	up := true
	for i, r := range rs {
		switch {
		case up && unicode.IsLetter(r):
			rs[i] = unicode.ToUpper(r)
			up = false
		case r == '-' || r == '.' || r == '(':
			up = true
		case unicode.IsLetter(r):
			up = false
		}
	}
	return string(rs)
}

var (
	oddQuotes = strings.NewReplacer(`"`, "'", "''", "'", "“", "'", "”", "'", "„", "'", "‟", "'", "‘", "'", "’", "'", "`", "'", "´", "'")
	forbidden = strings.NewReplacer(`\`, "-", "/", "-", "|", "-", ":", " -", "*", "", "?", "", "<", "", ">", "")
)

// assemble builds "Title - Author.ext", fitting FAT and the length limit by
// shortening the title, or "" when there is no title.
func assemble(title string, authors []string, ext string) string {
	title = safe(title)
	if title == "" {
		return ""
	}
	who := ""
	switch {
	case len(authors) == 1:
		who = authors[0]
	case len(authors) == 2:
		who = authors[0] + " & " + authors[1]
	case len(authors) > 2:
		who = authors[0] + " et al."
	}
	if who = safe(who); who != "" {
		who = " - " + who
	}
	if len(who)+len(ext) > MaxBytes/2 {
		who = ""
	}
	if room := MaxBytes - len(who) - len(ext); len(title) > room {
		title = shorten(title, room)
	}
	name := strings.TrimRight(title+who, " .") + ext
	if Valid(name) != nil {
		return ""
	}
	return name
}

// safe replaces what FAT cannot hold, straightens odd quotes and drops
// control characters.
func safe(s string) string {
	s = oddQuotes.Replace(s)
	s = forbidden.Replace(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	s = tidy(s)
	return strings.TrimLeft(strings.TrimRight(s, " ."), ". ")
}

// shorten cuts s to at most n bytes, at a word boundary when one is close.
func shorten(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(tidy(cut), " .-,")
}

// spaces collapses runs of white space.
func spaces(s string) string { return space.ReplaceAllString(strings.TrimSpace(s), " ") }
