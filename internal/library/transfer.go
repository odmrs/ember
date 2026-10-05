package library

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"
)

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

// Pending is an upload that passed its checks and has a part to write to.
// Its three steps let a caller hold a lock for the checks and the rename
// without holding it while the data streams in.
type Pending struct {
	l           *Library
	rel, dir    string
	name, final string
	size        int64
	opt         UploadOptions
	// Part is the Device path of the file the data goes to until the
	// rename: hidden, so no listing shows it, and named so that it cannot
	// be a file the user put there.
	Part string
}

// partPrefix and partSuffix frame the name of an upload's part: a dot, so
// nothing lists it and no name the validator lets through can match it,
// then random hex, so two uploads never share one. The Book's own name is
// left out, so a name of the longest length FAT allows still has a part.
const (
	partPrefix = ".ember-"
	partSuffix = ".part"
	partRandom = 8 // bytes
)

func partName() string {
	b := make([]byte, partRandom)
	_, _ = rand.Read(b)
	return partPrefix + hex.EncodeToString(b) + partSuffix
}

// IsPart reports whether name is one ember gives the part of an upload.
func IsPart(name string) bool {
	h, ok := strings.CutPrefix(name, partPrefix)
	if !ok {
		return false
	}
	h, ok = strings.CutSuffix(h, partSuffix)
	if !ok || len(h) != 2*partRandom {
		return false
	}
	return strings.Trim(h, "0123456789abcdef") == ""
}

// BeginUpload checks that size bytes named name can go into folder: the
// name, the place, that nothing already holds the name (unless replacing)
// and that it fits next to reserved bytes other uploads still have to
// write. It writes nothing.
func (l *Library) BeginUpload(folder, name string, size int64, reserved uint64, opt UploadOptions) (*Pending, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	if size < 0 {
		return nil, fmt.Errorf("%w: no size given", ErrIncomplete)
	}
	rel, dir, infos, err := l.uploadFolder(folder)
	if err != nil {
		return nil, err
	}
	final, _, err := l.target(dir, infos, name, opt)
	if err != nil {
		return nil, err
	}
	free, _, err := l.fs.FreeSpace(dir)
	if err != nil {
		return nil, deviceErr(err)
	}
	if need(size)+reserved > free {
		return nil, &NoSpaceError{Need: need(size), Free: free - min(free, reserved)}
	}
	return &Pending{l: l, rel: rel, dir: dir, name: name, final: final, size: size, opt: opt,
		Part: path.Join(dir, partName())}, nil
}

// Write streams the upload into its part. The part is created, never
// truncated: if its name is somehow taken, Write fails and leaves that file
// alone. Once the part exists, any error removes it.
func (u *Pending) Write(src io.Reader) error {
	w, err := u.l.fs.Create(u.Part)
	if err != nil {
		return deviceErr(err)
	}
	in := &sourceReader{r: src}
	// One byte past size tells a longer body from an exact one.
	n, err := io.Copy(w, &io.LimitedReader{R: in, N: u.size + 1})
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	switch {
	case in.err != nil:
		err = fmt.Errorf("%w: %v", ErrIncomplete, in.err)
	case err != nil:
		err = deviceErr(err)
	case n != u.size:
		err = fmt.Errorf("%w: got %d of %d bytes", ErrIncomplete, n, u.size)
	}
	if err != nil {
		_ = u.l.fs.Remove(u.Part)
	}
	return err
}

// Finish renames the written part into place and returns the Library path
// of the new file. The folder is read again first, since something may have
// taken the name while the data streamed in. On an error the part is
// removed.
func (u *Pending) Finish() (string, []Warning, error) {
	warns, err := u.l.place(u.dir, u.Part, u.name, u.final, u.opt)
	if err != nil {
		_ = u.l.fs.Remove(u.Part)
		return "", nil, err
	}
	return path.Join(u.rel, u.final), warns, nil
}

// Upload writes size bytes from src into folder under name: BeginUpload,
// Write and Finish in one go. Neither KOReader nor the Kindle's indexer ever
// sees half a file, and nothing is read from src until the checks pass.
func (l *Library) Upload(folder, name string, size int64, src io.Reader, opt UploadOptions) (string, []Warning, error) {
	u, err := l.BeginUpload(folder, name, size, 0, opt)
	if err != nil {
		return "", nil, err
	}
	if err := u.Write(src); err != nil {
		return "", nil, err
	}
	return u.Finish()
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
// Library and returns their Library paths. Only ember's own parts are taken:
// regular files whose name IsPart matches, outside Sidecars and hidden
// folders, never through a link. A part for which inUse returns true (a
// Device path) belongs to an upload still running and is kept.
func (l *Library) CleanParts(inUse func(string) bool) ([]string, error) {
	root, err := l.realRoot()
	if err != nil {
		return nil, err
	}
	var removed []string
	err = l.cleanParts(root, "", inUse, &removed)
	return removed, err
}

func (l *Library) cleanParts(dir, rel string, inUse func(string) bool, removed *[]string) error {
	infos, err := l.fs.ReadDir(dir)
	if err != nil {
		return deviceErr(err)
	}
	for _, fi := range infos {
		name, full := fi.Name(), path.Join(dir, fi.Name())
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
		case fi.Mode().IsRegular() && IsPart(name):
			if inUse != nil && inUse(full) {
				continue
			}
			if err := l.fs.Remove(full); err != nil {
				return deviceErr(err)
			}
			*removed = append(*removed, path.Join(rel, name))
		case strings.HasPrefix(name, "."), isSidecar(fi):
		case fi.IsDir():
			if err := l.cleanParts(full, path.Join(rel, name), inUse, removed); err != nil {
				return err
			}
		}
	}
	return nil
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
