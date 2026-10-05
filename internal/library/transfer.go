package library

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"
)

// partSuffix marks a file still being uploaded. The Kindle's indexer and
// KOReader see no Book until the finished file is renamed into place.
const partSuffix = ".part"

// UploadMargin is kept free on the Device beyond the file itself: FAT rounds
// every file up to whole clusters, and a full userstore upsets the Kindle.
const UploadMargin = 1 << 20

// Errors returned by transfers.
var (
	ErrNoSpace    = errors.New("library: not enough free space on the device")
	ErrIncomplete = errors.New("library: upload did not match its announced size")
	ErrNotFile    = errors.New("library: not a file")
)

// NoSpaceError is an upload refused for lack of room.
type NoSpaceError struct {
	Need, Free uint64
}

func (e *NoSpaceError) Error() string {
	return fmt.Sprintf("library: the upload needs %d bytes and the device has %d free", e.Need, e.Free)
}

func (e *NoSpaceError) Is(target error) bool { return target == ErrNoSpace }

// WarnReplacedLeft: the upload replaced a file, but the old one, already
// renamed aside, could not be deleted.
const WarnReplacedLeft = "replaced_left"

// UploadOptions changes what Upload allows.
type UploadOptions struct {
	// Replace allows writing over a file already holding the name. The new
	// file takes that file's exact name, so a Book keeps its Sidecar.
	Replace bool
	// ConfirmOpen allows replacing the Book KOReader has open.
	ConfirmOpen bool
}

// Arrival is what an upload of one file into a folder would meet.
type Arrival struct {
	Name string `json:"name"`
	// Reason is why ValidName refuses the name, or "".
	Reason string `json:"reason,omitempty"`
	// Existing is the entry that already holds the name, ignoring case.
	Existing string `json:"existing,omitempty"`
	// Replaceable says Existing is a file; a folder is never replaced.
	Replaceable bool `json:"replaceable,omitempty"`
	OpenNow     bool `json:"openNow,omitempty"`
	// Book says the name has a Book format; anything else is still
	// uploaded, but KOReader will not open it.
	Book      bool   `json:"book"`
	Fits      bool   `json:"fits"`
	FreeBytes uint64 `json:"freeBytes"`
}

// uploadFolder resolves and reads the folder an upload goes into. Hidden
// folders and Sidecars are KOReader's, not a place for Books.
func (l *Library) uploadFolder(folder string) (rel, dir string, infos []fs.FileInfo, err error) {
	if rel, err = clean(folder); err != nil {
		return "", "", nil, err
	}
	if unlisted(rel) {
		return "", "", nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	dir, infos, err = l.readFolder(rel)
	return rel, dir, infos, err
}

// CheckUpload says what uploading a file of size bytes named name into
// folder would meet, without writing anything.
func (l *Library) CheckUpload(folder, name string, size int64) (Arrival, error) {
	a := Arrival{Name: name}
	_, a.Book = BookFormat(name)
	var ne *NameError
	if err := ValidName(name); errors.As(err, &ne) {
		a.Reason = ne.Reason
	}
	_, dir, infos, err := l.uploadFolder(folder)
	if err != nil {
		return Arrival{}, err
	}
	if fi := takenBy(infos, name); fi != nil {
		a.Existing = fi.Name()
		a.Replaceable = fi.Mode().IsRegular()
		a.OpenNow = l.session().open == path.Join(dir, fi.Name())
	}
	free, _, err := l.fs.FreeSpace(dir)
	if err != nil {
		return Arrival{}, deviceErr(err)
	}
	a.FreeBytes = free
	a.Fits = size >= 0 && need(size) <= free
	return a, nil
}

func need(size int64) uint64 { return uint64(size) + UploadMargin }

// takenBy is the entry holding name, ignoring case as FAT does, or nil.
func takenBy(infos []fs.FileInfo, name string) fs.FileInfo {
	for _, fi := range infos {
		if strings.EqualFold(fi.Name(), name) {
			return fi
		}
	}
	return nil
}

// target is the name an upload ends up under: name itself, or the exact
// name of the file it replaces. existing is that file, or "".
func (l *Library) target(dir string, infos []fs.FileInfo, name string, opt UploadOptions) (final, existing string, err error) {
	fi := takenBy(infos, name)
	if fi == nil {
		return name, "", nil
	}
	if !opt.Replace || !fi.Mode().IsRegular() {
		return "", "", &CollisionError{Name: fi.Name()}
	}
	if !opt.ConfirmOpen && l.session().open == path.Join(dir, fi.Name()) {
		return "", "", fmt.Errorf("%w: %s", ErrOpenNow, fi.Name())
	}
	return fi.Name(), fi.Name(), nil
}

// sourceReader remembers why reading the upload failed, so a client that
// went away is told apart from a Device that did.
type sourceReader struct {
	r   io.Reader
	err error
}

func (s *sourceReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF && s.err == nil {
		s.err = err
	}
	return n, err
}

// Upload writes size bytes from src into folder under name. The data goes to
// "<name>.part" first and is renamed into place once complete, so neither
// KOReader nor the Kindle's indexer ever sees half a file. Nothing is read
// from src until the name, the place and the free space have been checked,
// and the name is checked again before the rename. It returns the Library
// path of the new file.
func (l *Library) Upload(folder, name string, size int64, src io.Reader, opt UploadOptions) (string, []Warning, error) {
	if err := ValidName(name); err != nil {
		return "", nil, err
	}
	if size < 0 {
		return "", nil, fmt.Errorf("%w: no size given", ErrIncomplete)
	}
	rel, dir, infos, err := l.uploadFolder(folder)
	if err != nil {
		return "", nil, err
	}
	final, _, err := l.target(dir, infos, name, opt)
	if err != nil {
		return "", nil, err
	}
	free, _, err := l.fs.FreeSpace(dir)
	if err != nil {
		return "", nil, deviceErr(err)
	}
	if need(size) > free {
		return "", nil, &NoSpaceError{Need: need(size), Free: free}
	}

	part := path.Join(dir, final+partSuffix)
	in := &sourceReader{r: src}
	// One byte past size tells a longer body from an exact one.
	n, err := l.fs.Create(part, &io.LimitedReader{R: in, N: size + 1})
	switch {
	case in.err != nil:
		err = fmt.Errorf("%w: %v", ErrIncomplete, in.err)
	case err != nil:
		err = deviceErr(err)
	case n != size:
		err = fmt.Errorf("%w: got %d of %d bytes", ErrIncomplete, n, size)
	}
	if err != nil {
		_ = l.fs.Remove(part)
		return "", nil, err
	}

	warns, err := l.place(dir, part, name, final, opt)
	if err != nil {
		_ = l.fs.Remove(part)
		return "", nil, err
	}
	return path.Join(rel, final), warns, nil
}

// place renames the finished part to its final name. The folder is read
// again first: something on the Device may have taken the name meanwhile. A
// file being replaced is renamed aside before the part takes its name, and
// put back if that fails, so the folder never ends up without either.
func (l *Library) place(dir, part, name, final string, opt UploadOptions) ([]Warning, error) {
	infos, err := l.fs.ReadDir(dir)
	if err != nil {
		return nil, deviceErr(err)
	}
	now, existing, err := l.target(dir, infos, name, opt)
	if err != nil {
		return nil, err
	}
	// A file the upload replaces may have gone or changed case meanwhile;
	// either way the name still belongs to this upload.
	if !strings.EqualFold(now, final) {
		return nil, &CollisionError{Name: now}
	}
	if existing == "" {
		return nil, l.run([]step{{part, path.Join(dir, final)}})
	}
	aside := path.Join(dir, tempName(existing, infos))
	if err := l.run([]step{{path.Join(dir, existing), aside}, {part, path.Join(dir, final)}}); err != nil {
		return nil, err
	}
	if err := l.fs.Remove(aside); err != nil {
		return []Warning{{Code: WarnReplacedLeft, Path: path.Base(aside)}}, nil
	}
	return nil, nil
}

// CleanParts deletes the parts that interrupted uploads left anywhere in the
// Library and returns their Library paths. Only what ember could have
// written is taken: a regular file named "<valid name>.part", outside
// Sidecars and hidden folders, never through a link.
func (l *Library) CleanParts() ([]string, error) {
	root, err := l.realRoot()
	if err != nil {
		return nil, err
	}
	var removed []string
	err = l.cleanParts(root, "", &removed)
	return removed, err
}

func (l *Library) cleanParts(dir, rel string, removed *[]string) error {
	infos, err := l.fs.ReadDir(dir)
	if err != nil {
		return deviceErr(err)
	}
	for _, fi := range infos {
		name := fi.Name()
		switch {
		case strings.HasPrefix(name, "."), fi.Mode()&fs.ModeSymlink != 0, isSidecar(fi):
		case fi.IsDir():
			if err := l.cleanParts(path.Join(dir, name), path.Join(rel, name), removed); err != nil {
				return err
			}
		case fi.Mode().IsRegular() && isPart(name):
			if err := l.fs.Remove(path.Join(dir, name)); err != nil {
				return deviceErr(err)
			}
			*removed = append(*removed, path.Join(rel, name))
		}
	}
	return nil
}

func isPart(name string) bool {
	stem, ok := strings.CutSuffix(name, partSuffix)
	return ok && ValidName(stem) == nil
}

// Download is an open file of the Library. The caller closes Body.
type Download struct {
	Name    string
	Size    int64
	ModTime time.Time
	Body    io.ReadCloser
}

// Download opens the file at rel for reading. Only what List shows can be
// downloaded: no folder, link, hidden file or Sidecar content.
func (l *Library) Download(rel string) (Download, error) {
	it, _, err := l.item(rel)
	if err != nil {
		return Download{}, err
	}
	if it.kind == KindFolder {
		return Download{}, fmt.Errorf("%w: %s", ErrNotFile, it.rel)
	}
	fi, err := l.fs.Stat(it.path())
	if err != nil {
		return Download{}, deviceErr(err)
	}
	body, err := l.fs.Open(it.path())
	if err != nil {
		return Download{}, deviceErr(err)
	}
	return Download{Name: it.name, Size: fi.Size(), ModTime: fi.ModTime(), Body: body}, nil
}
