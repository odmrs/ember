package pairing

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/odmrs/ember/internal/device"
	"github.com/odmrs/ember/internal/profile"
	"golang.org/x/crypto/ssh"
)

// Where KOReader keeps what ember reads and writes on a Kindle.
const (
	settingsPath       = "/mnt/us/koreader/settings.reader.lua"
	gitRevPath         = "/mnt/us/koreader/git-rev"
	authorizedKeysPath = "/mnt/us/koreader/settings/SSH/authorized_keys"
)

const maxSettingsSize = 4 << 20

// Survey reads what pairing needs to know about the Device. Missing settings
// are left empty; only a broken connection is an error.
func Survey(d *device.Device, libraryRoot string) (profile.Survey, error) {
	var s profile.Survey
	if rev, err := d.ReadFile(gitRevPath, 256); err == nil {
		s.KOReaderVersion = strings.TrimSpace(string(rev))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return s, err
	}

	settings, err := d.ReadFile(settingsPath, maxSettingsSize)
	if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, device.ErrTooLarge) {
		return s, err
	}
	src := string(settings)
	s.HomeDir, _ = luaString(src, "home_dir")
	s.MetadataMode, _ = luaString(src, "document_metadata_folder")
	s.LastFile, _ = luaString(src, "lastfile")

	fi, err := d.Stat(libraryRoot)
	switch {
	case err == nil:
		s.LibraryExists = fi.IsDir()
	case !errors.Is(err, fs.ErrNotExist):
		return s, err
	}
	if s.LibraryExists {
		if s.FreeBytes, s.TotalBytes, err = d.FreeSpace(libraryRoot); err != nil {
			return s, fmt.Errorf("free space: %w", err)
		}
	}
	return s, nil
}

// InstallKey adds key to the Device's authorized_keys unless it is already
// there. It only ever appends, creating the directory (0700) and the file
// (0600) when missing, so keys already on the Device survive.
func InstallKey(d *device.Device, key ssh.PublicKey, comment string) (added bool, err error) {
	existing, err := d.ReadFile(authorizedKeysPath, 1<<20)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if hasKey(existing, key) {
		return false, nil
	}
	if err := d.MkdirAll(path.Dir(authorizedKeysPath), 0o700); err != nil {
		return false, err
	}
	line := profile.AuthorizedKey(key) + " " + comment + "\n"
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		line = "\n" + line
	}
	if err := d.Append(authorizedKeysPath, []byte(line), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// hasKey reports whether an authorized_keys file lists key. Lines it cannot
// parse are skipped, not trusted.
func hasKey(file []byte, key ssh.PublicKey) bool {
	want := key.Marshal()
	for _, line := range bytes.Split(file, []byte("\n")) {
		k, _, _, _, err := ssh.ParseAuthorizedKey(line)
		if err == nil && bytes.Equal(k.Marshal(), want) {
			return true
		}
	}
	return false
}
