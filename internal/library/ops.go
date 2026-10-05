package library

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/odmrs/ember/internal/koreader"
)

// Errors returned by the write operations.
var (
	ErrExists       = errors.New("library: name already taken")
	ErrOpenNow      = errors.New("library: KOReader has this book open")
	ErrMetadataMode = errors.New("library: KOReader keeps reading progress where ember cannot carry it")
	ErrRoot         = errors.New("library: the library root cannot be changed")
	ErrIntoItself   = errors.New("library: a folder cannot go inside itself")
	ErrExtension    = errors.New("library: a book keeps its format")
	ErrPartial      = errors.New("library: operation left half done")
)

// CollisionError is a name taken at the destination. FAT ignores case, so
// "dune.epub" takes "Dune.epub".
type CollisionError struct {
	Name string // the entry already there
}

func (e *CollisionError) Error() string { return "library: name already taken by " + e.Name }

func (e *CollisionError) Is(target error) bool { return target == ErrExists }

// PartialError is a failed rename whose earlier steps could not be undone.
type PartialError struct {
	Err  error  // why the operation failed
	Undo error  // why putting things back failed
	At   string // Device path of what was left moved
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("library: %v; putting it back failed too (%v), so it is now at %s", e.Err, e.Undo, e.At)
}

func (e *PartialError) Is(target error) bool { return target == ErrPartial }

func (e *PartialError) Unwrap() error { return e.Err }

// MoveError is a batch move that stopped partway. The entries in Moved are
// in the destination, the one at Failed is where it was (or, when Err is a
// PartialError, where that says), and the ones after it were not tried.
type MoveError struct {
	Moved  []string // Library paths, as given, of the entries moved
	Failed string
	Err    error
}

func (e *MoveError) Error() string {
	return fmt.Sprintf("library: moved %s, then %s failed: %v", strings.Join(e.Moved, ", "), e.Failed, e.Err)
}

func (e *MoveError) Unwrap() error { return e.Err }

// Options changes what the write operations allow.
type Options struct {
	// ConfirmOpen allows touching the Book KOReader has open, or a folder
	// holding it. KOReader rewrites that Book's Sidecar at its old path when
	// it closes it.
	ConfirmOpen bool
}

// Warning is something an operation did differently from what was asked,
// without failing.
type Warning struct {
	Code string `json:"code"`
	Path string `json:"path"` // Library path of the item
}

// Warning codes.
const (
	// WarnSidecarShared: another Book in the folder uses the same Sidecar,
	// so it stayed where it was.
	WarnSidecarShared = "sidecar_shared"
	// WarnSidecarLeft: the Book was deleted but its Sidecar could not be.
	WarnSidecarLeft = "sidecar_left"
)

// Preview is what a delete would remove, counted recursively. Folders
// includes the folders asked for.
type Preview struct {
	Books   int  `json:"books"`
	Folders int  `json:"folders"`
	Others  int  `json:"others"`
	OpenNow bool `json:"openNow"`
}

// item is a listed entry an operation acts on.
type item struct {
	rel  string // Library path
	dir  string // real Device path of the folder holding it
	name string
	kind Kind
	// sidecar is the name of a Book's Sidecar in dir, or "".
	sidecar string
	// shared means another Book in dir has the same Sidecar name.
	shared bool
}

func (it item) path() string { return path.Join(it.dir, it.name) }

// item finds rel among the entries of its folder, which it also returns.
// Only what List shows can be acted on: links, hidden names, Sidecars and
// what is inside them are not found.
func (l *Library) item(rel string) (item, []fs.FileInfo, error) {
	rel, err := clean(rel)
	if err != nil {
		return item{}, nil, err
	}
	if rel == "" {
		return item{}, nil, ErrRoot
	}
	if unlisted(parent(rel)) {
		return item{}, nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	dir, infos, err := l.readFolder(parent(rel))
	if err != nil {
		return item{}, nil, err
	}
	name := path.Base(rel)
	for _, fi := range infos {
		if fi.Name() != name {
			continue
		}
		kind, _, ok := classify(fi)
		if !ok {
			break
		}
		it := item{rel: rel, dir: dir, name: name, kind: kind}
		if kind == KindBook {
			it.sidecar, it.shared = sidecarOf(name, infos)
		}
		return it, infos, nil
	}
	return item{}, nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
}

// unlisted reports whether the cleaned folder path rel goes through a
// hidden folder or a Sidecar, which List never shows.
func unlisted(rel string) bool {
	for _, folder := range strings.Split(rel, "/") {
		if strings.HasPrefix(folder, ".") || strings.HasSuffix(strings.ToLower(folder), ".sdr") {
			return true
		}
	}
	return false
}

// sidecarOf finds book's Sidecar among its siblings, and whether another
// Book there maps to the same Sidecar name, as Dune.epub and Dune.pdf do.
func sidecarOf(book string, siblings []fs.FileInfo) (sidecar string, shared bool) {
	want := SidecarName(book)
	for _, fi := range siblings {
		switch {
		case isSidecar(fi) && strings.EqualFold(fi.Name(), want):
			sidecar = fi.Name()
		case fi.Name() != book && fi.Mode().IsRegular():
			if _, ok := BookFormat(fi.Name()); ok && strings.EqualFold(SidecarName(fi.Name()), want) {
				shared = true
			}
		}
	}
	return sidecar, shared
}

// readFolder resolves the folder at rel and reads its entries.
func (l *Library) readFolder(rel string) (string, []fs.FileInfo, error) {
	dir, err := l.Resolve(rel)
	if err != nil {
		return "", nil, err
	}
	fi, err := l.fs.Stat(dir)
	if err != nil {
		return "", nil, deviceErr(err)
	}
	if !fi.IsDir() {
		return "", nil, ErrNotFolder
	}
	infos, err := l.fs.ReadDir(dir)
	if err != nil {
		return "", nil, deviceErr(err)
	}
	return dir, infos, nil
}

// session is what KOReader's settings say at the start of an operation.
type session struct {
	mode string
	open string // real Device path of the open Book, or ""
}

func (l *Library) session() session {
	s := l.settings()
	ses := session{mode: s.MetadataMode}
	if ses.mode == "" {
		ses.mode = koreader.MetadataDoc
	}
	if realRoot, err := l.realRoot(); err == nil {
		if rel := l.openNow(lastFile(s), realRoot); rel != "" {
			ses.open, _ = l.Resolve(rel)
		}
	}
	return ses
}

// isOpen reports whether it is the open Book or a folder holding it.
func (s session) isOpen(it item) bool {
	return s.open != "" && within(s.open, it.path())
}

// relocatable refuses to move or rename a Book, or a folder that may hold
// Books, when KOReader finds progress by the Book's path somewhere ember does
// not manage: the progress would stay behind, unlinked. Next to the Book
// ("doc") ember carries it; by content hash ("hash") it follows on its own.
func (s session) relocatable(it item) error {
	if it.kind == KindOther || s.mode == koreader.MetadataDoc || s.mode == koreader.MetadataHash {
		return nil
	}
	return fmt.Errorf("%w (metadata mode %q)", ErrMetadataMode, s.mode)
}

// step is one rename on the Device.
type step struct{ from, to string }

// stepsFor renames dir/from to toDir/to. A rename that only changes case
// goes through a temporary name: on FAT the target already exists, being
// the source itself, and sftp-server refuses to rename onto it.
func stepsFor(dir, from, toDir, to string, siblings []fs.FileInfo) []step {
	if dir == toDir && from != to && strings.EqualFold(from, to) {
		tmp := path.Join(dir, tempName(from, siblings))
		return []step{{path.Join(dir, from), tmp}, {tmp, path.Join(dir, to)}}
	}
	return []step{{path.Join(dir, from), path.Join(toDir, to)}}
}

// tempName keeps the original name in front, so anything left behind by a
// lost connection is recognisable in the listing.
func tempName(name string, siblings []fs.FileInfo) string {
	for {
		var b [3]byte
		_, _ = rand.Read(b[:])
		tmp := name + ".~" + hex.EncodeToString(b[:])
		if taken(siblings, tmp, "") == "" {
			return tmp
		}
	}
}

// taken returns the entry that already holds name, ignoring case as FAT
// does, or "". The entry named except is the item itself and does not count.
func taken(infos []fs.FileInfo, name, except string) string {
	for _, fi := range infos {
		if fi.Name() != except && strings.EqualFold(fi.Name(), name) {
			return fi.Name()
		}
	}
	return ""
}

// claims holds the names a batch has already planned to use at its
// destination, lower-cased.
type claims map[string]bool

func (c claims) take(dest []fs.FileInfo, name, except string) error {
	if n := taken(dest, name, except); n != "" {
		return &CollisionError{Name: n}
	}
	if c[strings.ToLower(name)] {
		return &CollisionError{Name: name}
	}
	c[strings.ToLower(name)] = true
	return nil
}

// relocate plans moving it to toDir under name, with its Sidecar, refusing
// any name already taken there. src are the entries of it.dir, dest those of
// toDir.
func relocate(it item, src []fs.FileInfo, toDir string, dest []fs.FileInfo, name string, c claims) ([]step, []Warning, error) {
	same := toDir == it.dir
	except := func(n string) string {
		if same {
			return n
		}
		return ""
	}
	if err := c.take(dest, name, except(it.name)); err != nil {
		return nil, nil, err
	}
	steps := stepsFor(it.dir, it.name, toDir, name, src)
	if it.sidecar == "" {
		return steps, nil, nil
	}
	if it.shared {
		return steps, []Warning{{Code: WarnSidecarShared, Path: it.rel}}, nil
	}
	sidecar := SidecarName(name)
	if err := c.take(dest, sidecar, except(it.sidecar)); err != nil {
		return nil, nil, err
	}
	if !same || sidecar != it.sidecar {
		steps = append(steps, stepsFor(it.dir, it.sidecar, toDir, sidecar, src)...)
	}
	return steps, nil, nil
}

// run applies steps in order. When one fails, the steps already done are
// undone in reverse, so a Book never ends up apart from its Sidecar.
func (l *Library) run(steps []step) error {
	for i, s := range steps {
		err := l.fs.Rename(s.from, s.to)
		if err == nil {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if uerr := l.fs.Rename(steps[j].to, steps[j].from); uerr != nil {
				return &PartialError{Err: err, Undo: uerr, At: steps[j].to}
			}
		}
		return deviceErr(err)
	}
	return nil
}

// CreateFolder makes the folder name inside parent and returns its Library
// path.
func (l *Library) CreateFolder(parent, name string) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	rel, err := clean(parent)
	if err != nil {
		return "", err
	}
	dir, infos, err := l.readFolder(rel)
	if err != nil {
		return "", err
	}
	if n := taken(infos, name, ""); n != "" {
		return "", &CollisionError{Name: n}
	}
	if err := l.fs.Mkdir(path.Join(dir, name)); err != nil {
		return "", deviceErr(err)
	}
	return path.Join(rel, name), nil
}

// Rename gives the entry at rel a new name in the same folder. A Book keeps
// its format and takes its Sidecar along.
func (l *Library) Rename(rel, name string, opt Options) ([]Warning, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	it, infos, err := l.item(rel)
	if err != nil {
		return nil, err
	}
	if it.kind == KindBook {
		was, _ := BookFormat(it.name)
		if now, ok := BookFormat(name); !ok || now != was {
			return nil, fmt.Errorf("%w: %s", ErrExtension, was)
		}
	}
	if name == it.name {
		return nil, nil
	}
	ses := l.session()
	if err := ses.relocatable(it); err != nil {
		return nil, err
	}
	steps, warns, err := relocate(it, infos, it.dir, infos, name, claims{})
	if err != nil {
		return nil, err
	}
	if ses.isOpen(it) && !opt.ConfirmOpen {
		return nil, fmt.Errorf("%w: %s", ErrOpenNow, it.rel)
	}
	return warns, l.run(steps)
}

// Move puts the entries at rels into the folder to. Every entry is checked
// before any is moved; each then moves with its Sidecar or not at all.
// Entries already in to are left alone.
func (l *Library) Move(rels []string, to string, opt Options) ([]Warning, error) {
	toRel, err := clean(to)
	if err != nil {
		return nil, err
	}
	toDir, dest, err := l.readFolder(toRel)
	if err != nil {
		return nil, err
	}
	ses := l.session()
	var (
		plans [][]step
		moves []string
		warns []Warning
		open  []string
		c     = claims{}
	)
	for _, rel := range rels {
		it, src, err := l.item(rel)
		if err != nil {
			return nil, err
		}
		if it.dir == toDir {
			continue
		}
		if it.kind == KindFolder && within(toDir, it.path()) {
			return nil, fmt.Errorf("%w: %s", ErrIntoItself, it.rel)
		}
		if err := ses.relocatable(it); err != nil {
			return nil, err
		}
		steps, w, err := relocate(it, src, toDir, dest, it.name, c)
		if err != nil {
			return nil, err
		}
		if ses.isOpen(it) {
			open = append(open, it.rel)
		}
		plans = append(plans, steps)
		moves = append(moves, rel)
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
			return warns, &MoveError{Moved: moves[:i], Failed: moves[i], Err: err}
		}
	}
	return warns, nil
}

// Delete removes the entries at rels: Books with their Sidecars, folders
// with everything in them. Every entry is checked before any is deleted.
func (l *Library) Delete(rels []string, opt Options) ([]Warning, error) {
	ses := l.session()
	var (
		items []item
		open  []string
	)
	for _, rel := range rels {
		it, _, err := l.item(rel)
		if err != nil {
			return nil, err
		}
		if ses.isOpen(it) {
			open = append(open, it.rel)
		}
		items = append(items, it)
	}
	if len(open) > 0 && !opt.ConfirmOpen {
		return nil, fmt.Errorf("%w: %s", ErrOpenNow, strings.Join(open, ", "))
	}
	var warns []Warning
	for _, it := range items {
		var err error
		if it.kind == KindFolder {
			err = l.removeAll(it.path())
		} else {
			err = l.fs.Remove(it.path())
		}
		if err != nil {
			return warns, deviceErr(err)
		}
		switch {
		case it.sidecar == "":
		case it.shared:
			warns = append(warns, Warning{Code: WarnSidecarShared, Path: it.rel})
		default:
			// The Book is gone either way; a Sidecar left behind only holds
			// progress nobody can open.
			if err := l.removeAll(path.Join(it.dir, it.sidecar)); err != nil {
				warns = append(warns, Warning{Code: WarnSidecarLeft, Path: it.rel})
			}
		}
	}
	return warns, nil
}

// removeAll deletes the directory dir and everything under it. ReadDir
// describes links as links, so a link is removed as itself and never
// followed, wherever it points.
func (l *Library) removeAll(dir string) error {
	infos, err := l.fs.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, fi := range infos {
		p := path.Join(dir, fi.Name())
		if fi.IsDir() && fi.Mode()&fs.ModeSymlink == 0 {
			err = l.removeAll(p)
		} else {
			err = l.fs.Remove(p)
		}
		if err != nil {
			return err
		}
	}
	return l.fs.Remove(dir)
}

// Preview counts what deleting the entries at rels would remove, so the
// dashboard can say so before asking.
func (l *Library) Preview(rels []string) (Preview, error) {
	ses := l.session()
	var p Preview
	for _, rel := range rels {
		it, _, err := l.item(rel)
		if err != nil {
			return Preview{}, err
		}
		p.OpenNow = p.OpenNow || ses.isOpen(it)
		switch it.kind {
		case KindBook:
			p.Books++
		case KindOther:
			p.Others++
		case KindFolder:
			p.Folders++
			if err := l.tally(it.path(), &p); err != nil {
				return Preview{}, deviceErr(err)
			}
		}
	}
	return p, nil
}

// tally counts what a folder holds the way List shows it: links, hidden
// names and Sidecars are not counted, and links are not followed.
func (l *Library) tally(dir string, p *Preview) error {
	infos, err := l.fs.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, fi := range infos {
		kind, _, ok := classify(fi)
		switch {
		case !ok:
		case kind == KindBook:
			p.Books++
		case kind == KindOther:
			p.Others++
		default:
			p.Folders++
			if err := l.tally(path.Join(dir, fi.Name()), p); err != nil {
				return err
			}
		}
	}
	return nil
}
