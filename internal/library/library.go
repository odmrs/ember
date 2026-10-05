// Package library holds the rules of the Library: which paths are inside it,
// what counts as a Folder, a Book or a Sidecar, and which Book KOReader has
// open. It works on any FS, so the Device and a local directory behave alike.
//
// Paths in and out of a Library are relative to its root, slash-separated and
// without a leading slash: "" is the root itself, "Sci-Fi/Dune.epub" a Book
// in it. Absolute paths never come from callers; Resolve is the only way to
// turn a Library path into a Device path.
package library

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/odmrs/ember/internal/koreader"
)

// FS is what a Library needs from the Device. Paths are absolute and
// slash-separated. device.Device implements it.
type FS interface {
	// ReadDir lists a directory, describing links as links.
	ReadDir(path string) ([]fs.FileInfo, error)
	// Stat follows links.
	Stat(path string) (fs.FileInfo, error)
	// RealPath resolves "." and ".." and every link on the Device itself,
	// accepting a last component that does not exist yet.
	RealPath(path string) (string, error)
	ReadFile(path string, max int64) ([]byte, error)
	FreeSpace(path string) (free, total uint64, err error)
	// Mkdir creates one directory; it fails if path exists.
	Mkdir(path string) error
	// Rename moves a file or directory. It may replace an existing target,
	// so the Library checks for one first.
	Rename(from, to string) error
	// Remove deletes a file, a link (never what it points to) or an empty
	// directory.
	Remove(path string) error
	// Create creates a new file at path for writing. It fails if path
	// already exists and never truncates. Copying into it streams: nothing
	// holds the whole file in memory.
	Create(path string) (io.WriteCloser, error)
	// Open opens a file for reading.
	Open(path string) (io.ReadCloser, error)
}

// Errors returned for Library paths.
var (
	ErrOutside   = errors.New("library: path is outside the library")
	ErrNotFound  = errors.New("library: no such file or folder")
	ErrNotFolder = errors.New("library: not a folder")
)

// Kind is what an Entry is.
type Kind string

const (
	KindFolder Kind = "folder"
	KindBook   Kind = "book"
	// KindOther is a file that is not a Book. It is listed, so nothing on the
	// Device takes space unseen, but KOReader will not open it.
	KindOther Kind = "other"
)

// Entry is one item in a Folder.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"` // Library path
	Kind Kind   `json:"kind"`
	// Format is the upper-case extension: the Book format, or the plain
	// extension of an other file. Empty for folders.
	Format  string    `json:"format,omitempty"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	// Items and Subfolders count a folder's visible entries and folders
	// among them; both are -1 when the folder could not be read.
	Items      int  `json:"items,omitempty"`
	Subfolders int  `json:"subfolders,omitempty"`
	HasSidecar bool `json:"hasSidecar,omitempty"`
	OpenNow    bool `json:"openNow,omitempty"`
}

// Folder is a listed folder: folders first, then files, each by name.
type Folder struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
	Books   int     `json:"books"`
	Folders int     `json:"folders"`
	Others  int     `json:"others"`
}

// Stats describes the Library as a whole.
type Stats struct {
	Root       string `json:"root"`
	FreeBytes  uint64 `json:"freeBytes"`
	TotalBytes uint64 `json:"totalBytes"`
	// MetadataMode is where KOReader keeps Sidecars: koreader.MetadataDoc
	// unless the user changed it.
	MetadataMode string `json:"metadataMode"`
	// OpenNow is the Library path of the Book KOReader has open, or "".
	OpenNow string `json:"openNow"`
}

// Library is a Library root on an FS. It is cheap to make and holds no
// state, so one can be made per request.
type Library struct {
	fs   FS
	root string
}

// New returns the Library rooted at root, an absolute Device path.
func New(fsys FS, root string) *Library {
	return &Library{fs: fsys, root: path.Clean("/" + root)}
}

// countWorkers bounds the folders counted at once while listing.
const countWorkers = 8

// Resolve turns a Library path into the Device path it really names, with
// links resolved by the Device. Absolute paths, "..", backslashes and NUL
// bytes are refused outright, and so is anything that resolves outside the
// root. The last component does not have to exist.
func (l *Library) Resolve(rel string) (string, error) {
	rel, err := clean(rel)
	if err != nil {
		return "", err
	}
	realRoot, err := l.realRoot()
	if err != nil {
		return "", err
	}
	real, err := l.fs.RealPath(path.Join(l.root, rel))
	if err != nil {
		return "", deviceErr(err)
	}
	real = path.Clean(real)
	if !within(real, realRoot) {
		return "", ErrOutside
	}
	return real, nil
}

// clean refuses what Resolve refuses before asking the Device anything, and
// returns rel without leading, trailing or doubled slashes.
func clean(rel string) (string, error) {
	if strings.HasPrefix(rel, "/") || strings.ContainsAny(rel, "\\\x00") {
		return "", ErrOutside
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", ErrOutside
		}
	}
	return strings.Trim(path.Clean("/"+rel), "/"), nil
}

// parent is the Library path of the folder holding rel.
func parent(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

func (l *Library) realRoot() (string, error) {
	real, err := l.fs.RealPath(l.root)
	if err != nil {
		return "", deviceErr(err)
	}
	return path.Clean(real), nil
}

func within(p, dir string) bool {
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}

// deviceErr maps a missing file to ErrNotFound and keeps every other error.
func deviceErr(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}

// List lists the folder at rel. Sidecars, hidden files and links are left
// out; subfolders are counted so the dashboard can show their size.
func (l *Library) List(rel string) (Folder, error) {
	dir, infos, err := l.readFolder(rel)
	if err != nil {
		return Folder{}, err
	}
	rel, _ = clean(rel)
	open := lastFile(l.settings())
	sidecars := map[string]bool{}
	for _, fi := range infos {
		if isSidecar(fi) {
			sidecars[strings.ToLower(fi.Name())] = true
		}
	}

	f := Folder{Path: rel, Entries: []Entry{}}
	for _, fi := range infos {
		kind, format, ok := classify(fi)
		if !ok {
			continue
		}
		e := Entry{
			Name: fi.Name(), Path: path.Join(rel, fi.Name()), Kind: kind, Format: format,
			Size: fi.Size(), ModTime: fi.ModTime(),
		}
		switch kind {
		case KindFolder:
			e.Size = 0
			f.Folders++
		case KindBook:
			e.HasSidecar = sidecars[strings.ToLower(SidecarName(fi.Name()))]
			full := path.Join(dir, fi.Name())
			e.OpenNow = open != "" && (open == full || open == path.Join(l.root, e.Path))
			f.Books++
		default:
			f.Others++
		}
		f.Entries = append(f.Entries, e)
	}
	l.count(dir, f.Entries)
	slices.SortStableFunc(f.Entries, func(a, b Entry) int {
		if (a.Kind == KindFolder) != (b.Kind == KindFolder) {
			if a.Kind == KindFolder {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Name, b.Name))
	})
	return f, nil
}

// count fills in Items and Subfolders for the folders among entries, reading
// several at once: each is a round trip to the Device.
func (l *Library) count(dir string, entries []Entry) {
	sem := make(chan struct{}, countWorkers)
	var wg sync.WaitGroup
	for i := range entries {
		e := &entries[i]
		if e.Kind != KindFolder {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			infos, err := l.fs.ReadDir(path.Join(dir, e.Name))
			if err != nil {
				e.Items, e.Subfolders = -1, -1
				return
			}
			for _, fi := range infos {
				if kind, _, ok := classify(fi); ok {
					e.Items++
					if kind == KindFolder {
						e.Subfolders++
					}
				}
			}
		}()
	}
	wg.Wait()
}

// classify says what a directory entry is, and whether it is listed at all.
// Hidden names and Sidecars are KOReader's business, links could lead out
// of the Library, and FAT holds nothing else that a user put there.
func classify(fi fs.FileInfo) (kind Kind, format string, listed bool) {
	name := fi.Name()
	switch {
	case strings.HasPrefix(name, "."), fi.Mode()&fs.ModeSymlink != 0:
		return "", "", false
	case fi.IsDir():
		if isSidecar(fi) {
			return "", "", false
		}
		return KindFolder, "", true
	case !fi.Mode().IsRegular():
		return "", "", false
	}
	if format, ok := BookFormat(name); ok {
		return KindBook, format, true
	}
	return KindOther, strings.ToUpper(strings.TrimPrefix(path.Ext(name), ".")), true
}

func isSidecar(fi fs.FileInfo) bool {
	return fi.IsDir() && strings.HasSuffix(strings.ToLower(fi.Name()), ".sdr")
}

// SidecarName is the name of the Sidecar KOReader keeps next to a Book in
// "doc" metadata mode: the Book's name without its last extension, plus
// ".sdr".
func SidecarName(book string) string {
	return strings.TrimSuffix(book, path.Ext(book)) + ".sdr"
}

// Book formats by extension. KOReader opens the first group through
// crengine, MuPDF and DjVuLibre. AZW3 and KFX are the Kindle's own formats:
// KOReader cannot open them, but they are books and their Sidecars must
// travel with them all the same. Images, which MuPDF also opens, are left as
// other files.
var bookFormats = map[string]bool{
	"epub": true, "epub3": true, "fb2": true, "fb3": true, "mobi": true, "azw": true,
	"doc": true, "docx": true, "odt": true, "rtf": true, "chm": true, "pdb": true, "prc": true,
	"html": true, "htm": true, "xhtml": true, "txt": true, "md": true,
	"fb2.zip": true, "txt.zip": true, "html.zip": true, "htm.zip": true, "rtf.zip": true, "md.zip": true,
	"pdf": true, "xps": true, "cbz": true, "cbt": true, "cbr": true, "svg": true, "djvu": true,
	"azw3": true, "kfx": true,
}

// BookFormat returns the upper-case format of a Book name, such as "EPUB" or
// "FB2.ZIP", and false for any other name.
func BookFormat(name string) (string, bool) {
	lower := strings.ToLower(name)
	// The first dot gives the longest extension, so "fb2.zip" wins over "zip".
	for i := 1; i < len(lower); i++ {
		if lower[i] == '.' && bookFormats[lower[i+1:]] {
			return strings.ToUpper(lower[i+1:]), true
		}
	}
	return "", false
}

// Stats reports the free space of the Library's file system, KOReader's
// metadata mode, and the Book it has open.
func (l *Library) Stats() (Stats, error) {
	root, err := l.realRoot()
	if err != nil {
		return Stats{}, err
	}
	fi, err := l.fs.Stat(root)
	if err != nil {
		return Stats{}, deviceErr(err)
	}
	if !fi.IsDir() {
		return Stats{}, ErrNotFolder
	}
	st := Stats{Root: l.root, MetadataMode: koreader.MetadataDoc}
	if st.FreeBytes, st.TotalBytes, err = l.fs.FreeSpace(root); err != nil {
		return Stats{}, deviceErr(err)
	}
	settings := l.settings()
	if settings.MetadataMode != "" {
		st.MetadataMode = settings.MetadataMode
	}
	st.OpenNow = l.openNow(lastFile(settings), root)
	return st, nil
}

// openNow maps KOReader's lastfile to a Library path when it names a file
// inside the Library that still exists.
func (l *Library) openNow(p, realRoot string) string {
	if p == "" {
		return ""
	}
	var rel string
	switch {
	case within(p, realRoot) && p != realRoot:
		rel = strings.TrimPrefix(p, realRoot+"/")
	case within(p, l.root) && p != l.root:
		rel = strings.TrimPrefix(p, l.root+"/")
	default:
		return ""
	}
	real, err := l.Resolve(rel)
	if err != nil {
		return ""
	}
	if fi, err := l.fs.Stat(real); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return rel
}

// lastFile is KOReader's lastfile, cleaned, or "" when it is not an
// absolute path.
func lastFile(s koreader.Settings) string {
	p := s.LastFile
	if !strings.HasPrefix(p, "/") {
		return ""
	}
	return path.Clean(p)
}

// settings reads KOReader's settings. A missing, oversized or unreadable
// file gives empty settings: nothing in the Library depends on them.
func (l *Library) settings() koreader.Settings {
	src, err := l.fs.ReadFile(koreader.SettingsPath, koreader.MaxSettingsSize)
	if err != nil {
		return koreader.Settings{}
	}
	return koreader.ParseSettings(src)
}
