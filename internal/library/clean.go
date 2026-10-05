package library

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/odmrs/ember/internal/bookmeta"
	"github.com/odmrs/ember/internal/names"
)

// Proposal is a clean name proposed for one Book. Nothing is renamed until
// the proposals the user keeps come back to RenameAll.
type Proposal struct {
	Path     string `json:"path"` // Library path
	Name     string `json:"name"`
	Proposed string `json:"proposed"`
	// Source is names.SourceMetadata, SourcePattern or SourceGeneric; empty
	// when nothing changes.
	Source  string `json:"source,omitempty"`
	Changed bool   `json:"changed"`
	// Taken is the entry already holding Proposed, or its Sidecar's name,
	// ignoring case; another Book proposed the same name counts too. The
	// rename would be refused.
	Taken   string `json:"taken,omitempty"`
	OpenNow bool   `json:"openNow,omitempty"`
}

// cleanWorkers bounds the Books whose metadata is read at once. Each read
// is a few round trips to the Device, which also serves the dashboard.
const cleanWorkers = 3

// metaFormats maps the Book formats bookmeta reads to the name it knows
// them by.
var metaFormats = map[string]string{
	"EPUB": "EPUB", "EPUB3": "EPUB", "MOBI": "MOBI", "AZW": "AZW", "AZW3": "AZW3", "PRC": "PRC", "PDB": "PDB", "PDF": "PDF",
}

// ProposeNames proposes clean names for the Books in the folder at rel, or
// for the one Book at rel. It reads each Book's metadata, at most
// bookmeta.MaxRead bytes of it, and renames nothing.
func (l *Library) ProposeNames(rel string) ([]Proposal, error) {
	rel, err := clean(rel)
	if err != nil {
		return nil, err
	}
	if unlisted(rel) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	folder, infos, err := l.readFolder(rel)
	var books []fs.FileInfo
	switch {
	case err == nil:
		for _, fi := range infos {
			if kind, _, ok := classify(fi); ok && kind == KindBook {
				books = append(books, fi)
			}
		}
	case errors.Is(err, ErrNotFolder):
		it, siblings, err := l.item(rel)
		if err != nil {
			return nil, err
		}
		if it.kind != KindBook {
			return []Proposal{}, nil
		}
		rel, folder, infos = parent(it.rel), it.dir, siblings
		for _, fi := range siblings {
			if fi.Name() == it.name {
				books = append(books, fi)
			}
		}
	default:
		return nil, err
	}

	ps := make([]Proposal, len(books))
	sem := make(chan struct{}, cleanWorkers)
	var wg sync.WaitGroup
	for i, fi := range books {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			p := Proposal{Path: path.Join(rel, fi.Name()), Name: fi.Name()}
			p.Proposed, p.Source, p.Changed = names.Clean(fi.Name(), l.metadata(path.Join(folder, fi.Name()), fi))
			ps[i] = p
		}()
	}
	wg.Wait()

	ses := l.session()
	c := claims{}
	for i := range ps {
		p := &ps[i]
		it := item{rel: p.Path, dir: folder, name: p.Name, kind: KindBook}
		it.sidecar, it.shared = sidecarOf(p.Name, infos)
		p.OpenNow = ses.isOpen(it)
		if p.Changed {
			if _, _, err := relocate(it, infos, folder, infos, p.Proposed, c); err != nil {
				p.Taken = takenName(err)
			}
		}
	}
	return ps, nil
}

// takenName is the name a refused rename collided with.
func takenName(err error) string {
	var ce *CollisionError
	if errors.As(err, &ce) {
		return ce.Name
	}
	return err.Error()
}

// metadata reads what the Book at p says about itself. A Book that cannot
// be read leaves only its file name to go by.
func (l *Library) metadata(p string, fi fs.FileInfo) names.Metadata {
	book, _ := BookFormat(fi.Name())
	format, ok := metaFormats[book]
	if !ok {
		return names.Metadata{}
	}
	f, err := l.fs.Open(p)
	if err != nil {
		return names.Metadata{}
	}
	defer f.Close()
	m, err := bookmeta.Read(f, fi.Size(), format)
	if err != nil {
		return names.Metadata{}
	}
	return names.Metadata{Title: m.Title, Authors: m.Authors}
}

// UploadName is the clean name an upload named name lands under. Only Books
// are cleaned, and only by their file name: the file is not on the Device
// yet.
func UploadName(name string) string {
	if _, ok := BookFormat(name); !ok {
		return name
	}
	clean, _, _ := names.Clean(name, names.Metadata{})
	return clean
}

// NameChange is one rename of a batch.
type NameChange struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// RenameError is a batch rename that stopped partway. The entries in
// Renamed have their new names, the one at Failed kept its own (or, when
// Err is a PartialError, is where that says), and those after it were not
// tried.
type RenameError struct {
	Renamed []string // Library paths, as given, of the entries renamed
	Failed  string
	Err     error
}

func (e *RenameError) Error() string {
	return fmt.Sprintf("library: renamed %s, then %s failed: %v", strings.Join(e.Renamed, ", "), e.Failed, e.Err)
}

func (e *RenameError) Unwrap() error { return e.Err }

// RenameAll renames several entries, each in its own folder, as Rename
// does. Every rename is checked before any is made, names claimed by the
// batch included, so a refusal leaves everything as it was.
func (l *Library) RenameAll(changes []NameChange, opt Options) ([]Warning, error) {
	ses := l.session()
	var (
		plans   [][]step
		renames []string
		warns   []Warning
		open    []string
		byDir   = map[string]claims{}
	)
	for _, ch := range changes {
		if err := ValidName(ch.Name); err != nil {
			return nil, err
		}
		it, infos, err := l.item(ch.Path)
		if err != nil {
			return nil, err
		}
		if it.kind == KindBook {
			was, _ := BookFormat(it.name)
			if now, ok := BookFormat(ch.Name); !ok || now != was {
				return nil, fmt.Errorf("%w: %s", ErrExtension, was)
			}
		}
		if ch.Name == it.name {
			continue
		}
		if err := ses.relocatable(it); err != nil {
			return nil, err
		}
		c := byDir[it.dir]
		if c == nil {
			c = claims{}
			byDir[it.dir] = c
		}
		steps, w, err := relocate(it, infos, it.dir, infos, ch.Name, c)
		if err != nil {
			return nil, err
		}
		if ses.isOpen(it) {
			open = append(open, it.rel)
		}
		plans = append(plans, steps)
		renames = append(renames, ch.Path)
		warns = append(warns, w...)
	}
	if len(open) > 0 && !opt.ConfirmOpen {
		return nil, fmt.Errorf("%w: %s", ErrOpenNow, strings.Join(open, ", "))
	}
	for i, steps := range plans {
		if err := l.run(steps); err != nil {
			if i == 0 {
				return nil, err
			}
			return warns, &RenameError{Renamed: renames[:i], Failed: renames[i], Err: err}
		}
	}
	return warns, nil
}
