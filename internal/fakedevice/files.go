package fakedevice

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/pkg/sftp"
)

// files serves SFTP requests from a host directory standing in for the
// Device's root. Every request path is cleaned as an absolute path before it
// is joined to root, so ".." cannot climb out of it.
type files struct {
	root string
	free *atomic.Uint64
}

func handlers(f *files) sftp.Handlers {
	return sftp.Handlers{FileGet: f, FilePut: f, FileCmd: f, FileList: f}
}

func (f *files) host(p string) string {
	return filepath.Join(f.root, filepath.FromSlash(path.Clean("/"+p)))
}

func (f *files) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	return os.Open(f.host(r.Filepath))
}

// Filewrite never opens with O_APPEND: os.File refuses WriteAt on such files,
// and SFTP clients send explicit offsets anyway.
func (f *files) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	fl := r.Pflags()
	mode := os.O_WRONLY
	if fl.Creat {
		mode |= os.O_CREATE
	}
	if fl.Trunc {
		mode |= os.O_TRUNC
	}
	if fl.Excl {
		mode |= os.O_EXCL
	}
	return os.OpenFile(f.host(r.Filepath), mode, 0o644)
}

func (f *files) Filecmd(r *sftp.Request) error {
	p := f.host(r.Filepath)
	switch r.Method {
	case "Setstat":
		if r.AttrFlags().Permissions {
			return os.Chmod(p, r.Attributes().FileMode().Perm())
		}
		return nil
	case "Rename":
		return os.Rename(p, f.host(r.Target))
	case "Rmdir", "Remove":
		return os.Remove(p)
	case "Mkdir":
		return os.Mkdir(p, 0o755)
	}
	return sftp.ErrSSHFxOpUnsupported
}

func (f *files) StatVFS(r *sftp.Request) (*sftp.StatVFS, error) {
	if _, err := os.Stat(f.host(r.Filepath)); err != nil {
		return nil, err
	}
	const block = 4096
	free := f.free.Load() / block
	return &sftp.StatVFS{Bsize: block, Frsize: block, Blocks: TotalSpace / block, Bfree: free, Bavail: free, Namemax: 255}, nil
}

func (f *files) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	p := f.host(r.Filepath)
	switch r.Method {
	case "Stat":
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		return lister{fi}, nil
	case "List":
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		infos := make(lister, 0, len(entries))
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil {
				return nil, err
			}
			infos = append(infos, fi)
		}
		return infos, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}

// Lstat describes a symbolic link itself rather than its target.
func (f *files) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	fi, err := os.Lstat(f.host(r.Filepath))
	if err != nil {
		return nil, err
	}
	return lister{fi}, nil
}

// RealPath resolves symbolic links like OpenSSH's sftp-server does, including
// accepting a last component that does not exist yet. Links on the fake are
// written with host paths, so they are resolved on the host and mapped back.
func (f *files) RealPath(p string) (string, error) {
	p = path.Clean("/" + p)
	root, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		return "", err
	}
	host, err := filepath.EvalSymlinks(f.host(p))
	if errors.Is(err, fs.ErrNotExist) {
		if _, lerr := os.Lstat(f.host(p)); lerr == nil {
			return "", err // a dangling link
		}
		parent, perr := filepath.EvalSymlinks(f.host(path.Dir(p)))
		if perr != nil {
			return "", perr
		}
		host, err = filepath.Join(parent, path.Base(p)), nil
	}
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, host)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", os.ErrPermission
	}
	return path.Join("/", filepath.ToSlash(rel)), nil
}

type lister []os.FileInfo

func (l lister) ListAt(out []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(out, l[off:])
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}

var (
	_ sftp.StatVFSFileCmder   = (*files)(nil)
	_ sftp.LstatFileLister    = (*files)(nil)
	_ sftp.RealPathFileLister = (*files)(nil)
)
