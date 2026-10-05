// Package profile stores ember's local configuration: the paired Device, what
// was learned about it, and ember's own SSH key. Nothing here leaves the
// machine.
package profile

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Defaults for a KOReader SSH server on a Kindle.
const (
	DefaultPort        = 2222
	DefaultUser        = "root"
	DefaultLibraryRoot = "/mnt/us/documents"
)

const (
	profileFile = "profile.json"
	keyFile     = "id_ed25519"
)

// ErrNotPaired is returned by Load when no Device has been paired yet.
var ErrNotPaired = errors.New("profile: no device paired")

// Profile is the paired Device and the preferences that go with it.
type Profile struct {
	Address     string `json:"address"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	LibraryRoot string `json:"libraryRoot"`
	// HostKey is the Device's pinned host key in authorized_keys format.
	HostKey      string `json:"hostKey"`
	Survey       Survey `json:"survey"`
	KeepAwake    bool   `json:"keepAwake"`
	Passwordless bool   `json:"passwordless"`
	// RawUploadNames keeps an uploaded Book's own file name instead of the
	// clean one. False, the default, cleans: a Profile from before the
	// setting existed reads the same way.
	RawUploadNames bool      `json:"rawUploadNames"`
	PairedAt       time.Time `json:"pairedAt"`
}

// Survey is what ember read from the Device while pairing.
type Survey struct {
	KOReaderVersion string `json:"koreaderVersion"`
	// HomeDir, MetadataMode and LastFile are KOReader settings; empty means
	// the setting is absent and KOReader uses its default.
	HomeDir       string `json:"homeDir"`
	MetadataMode  string `json:"metadataMode"`
	LastFile      string `json:"lastFile"`
	LibraryExists bool   `json:"libraryExists"`
	FreeBytes     uint64 `json:"freeBytes"`
	TotalBytes    uint64 `json:"totalBytes"`
}

// New returns a Profile for the Device at address with every other field at
// its default.
func New(address string, port int) Profile {
	return Profile{
		Address:     address,
		Port:        port,
		User:        DefaultUser,
		LibraryRoot: DefaultLibraryRoot,
		KeepAwake:   true,
	}
}

// HostPort is the Device's address in the form net.Dial expects.
func (p Profile) HostPort() string {
	return fmt.Sprintf("%s:%d", p.Address, p.Port)
}

// Store is a Profile directory on disk.
type Store struct {
	dir string
	mu  sync.Mutex
}

// DefaultDir is ember's directory inside the OS user config dir.
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ember"), nil
}

// Open returns the Store at dir, creating it with mode 0700 if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Load reads the saved Profile, or returns ErrNotPaired.
func (s *Store) Load() (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Save replaces the saved Profile atomically.
func (s *Store) Save(p Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(p)
}

// Update applies change to the saved Profile and saves the result, with no
// other Load or Save in between. Nothing is saved if change fails.
func (s *Store) Update(change func(*Profile) error) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.load()
	if err != nil {
		return Profile{}, err
	}
	if err := change(&p); err != nil {
		return Profile{}, err
	}
	if err := s.save(p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// Forget deletes the Profile and ember's key pair. The forgotten Device may
// still list the public key in its authorized_keys, but the private half is
// gone, so that entry opens nothing; the next Signer call makes a new pair.
func (s *Store) Forget() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range []string{profileFile, keyFile} {
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) load() (Profile, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, profileFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Profile{}, ErrNotPaired
	}
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, fmt.Errorf("profile: %s is corrupt: %w", profileFile, err)
	}
	return p, nil
}

func (s *Store) save(p Profile) error {
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, profileFile), append(raw, '\n'))
}

// Signer returns ember's SSH key, generating it on first use. The key is
// kept until Forget: the paired Device trusts it.
func (s *Store) Signer() (ssh.Signer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, keyFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		raw, err = generateKey()
		if err == nil {
			err = writeAtomic(path, raw)
		}
	}
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(raw)
}

// PublicKey returns ember's public key as an authorized_keys line, without
// the trailing newline.
func (s *Store) PublicKey() (string, error) {
	signer, err := s.Signer()
	if err != nil {
		return "", err
	}
	return AuthorizedKey(signer.PublicKey()) + " ember", nil
}

// AuthorizedKey formats key as "<type> <base64>".
func AuthorizedKey(key ssh.PublicKey) string {
	line := ssh.MarshalAuthorizedKey(key)
	return string(line[:len(line)-1])
}

func generateKey() ([]byte, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "ember")
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(block), nil
}

// writeAtomic writes data to a temp file in the same directory and renames
// it over path, so a crash never leaves a half-written file. CreateTemp makes
// the file with mode 0600.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
