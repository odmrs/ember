package library

import (
	"errors"
	"path"
	"strings"
)

// UserStorage is the Kindle's user-visible storage, the FAT partition a
// computer sees over USB. A Library root must be a folder inside it: anything
// else is the Kindle's own system.
const UserStorage = "/mnt/us"

// maxRootLen bounds a Library root path, well above any real one.
const maxRootLen = 1024

// ErrRootOutside means a proposed Library root is not a folder inside
// UserStorage, as written or once links are followed.
var ErrRootOutside = errors.New("library: the library root must be a folder inside " + UserStorage)

// CheckRoot validates root as a new Library root on fsys and returns it
// cleaned. It must be an absolute path strictly inside UserStorage, with no
// ".." and nothing a shell or FAT would read differently, and it must name an
// existing folder that does not lead outside UserStorage through a link.
func CheckRoot(fsys FS, root string) (string, error) {
	if len(root) > maxRootLen || !strings.HasPrefix(root, "/") || strings.ContainsAny(root, "\\") {
		return "", ErrRootOutside
	}
	for _, r := range root {
		if r < 0x20 || r == 0x7f {
			return "", ErrRootOutside
		}
	}
	for _, part := range strings.Split(root, "/") {
		if part == ".." {
			return "", ErrRootOutside
		}
	}
	clean := path.Clean(root)
	if !strings.HasPrefix(clean, UserStorage+"/") {
		return "", ErrRootOutside
	}

	real, err := fsys.RealPath(clean)
	if err != nil {
		return "", deviceErr(err)
	}
	if !strings.HasPrefix(path.Clean(real), UserStorage+"/") {
		return "", ErrRootOutside
	}
	fi, err := fsys.Stat(clean)
	if err != nil {
		return "", deviceErr(err)
	}
	if !fi.IsDir() {
		return "", ErrNotFolder
	}
	return clean, nil
}
