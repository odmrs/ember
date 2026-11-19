// Package bookget searches, checks and saves ebooks the user is authorized to obtain.
package bookget

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"bookget/checker"
	"bookget/ember"
	"bookget/safehttp"
	"bookget/source"
	"bookget/virustotal"
)

type Options struct {
	Format, OutputDir string
	Send              bool
}
type Result struct {
	Path     string   `json:"path"`
	Sent     bool     `json:"sent"`
	Messages []string `json:"messages"`
}
type Converter func(context.Context, string, string) error
type App struct {
	Sources []source.Source
	VT      *virustotal.Client
	Ember   *ember.Client
	Convert Converter
}

func Calibre(ctx context.Context, input, output string) error {
	binary, err := exec.LookPath("ebook-convert")
	if err != nil {
		return fmt.Errorf("Calibre ebook-convert is missing: install Calibre from https://calibre-ebook.com/download and add ebook-convert to PATH")
	}
	cmd := exec.CommandContext(ctx, binary, input, output)
	// Never inherit secrets such as VT_API_KEY into a converter subprocess.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Dir(input), "TMPDIR=" + filepath.Dir(input)}
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("ebook-convert failed: %w", err)
	}
	return nil
}
func ValidFormat(f string) bool { return f == "epub" || f == "azw3" || f == "pdf" }
func Filename(title, author, format string) string {
	clean := func(s string) string {
		return strings.Trim(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
				return '-'
			}
			return r
		}, s), " .-")
	}
	title = clean(title)
	author = clean(author)
	if title == "" {
		title = "Untitled"
	}
	if author == "" {
		author = "Unknown author"
	}
	name := title + " - " + author
	// Leave space for extension and avoid splitting UTF-8 sequences.
	for len(name) > 220 {
		r := []rune(name)
		name = string(r[:len(r)-1])
	}
	return name + "." + format
}
func (a *App) check(ctx context.Context, path, format string) (checker.Report, []string, error) {
	report, err := checker.Check(path, format)
	messages := append([]string(nil), report.Findings...)
	if err != nil {
		return report, messages, err
	}
	if a.VT != nil {
		message, e := a.VT.Check(ctx, path)
		if message != "" {
			messages = append(messages, message)
		}
		if e != nil {
			return report, messages, e
		}
	}
	return report, messages, nil
}
func (a *App) Get(ctx context.Context, id string, opt Options) (Result, error) {
	result := Result{Messages: []string{}}
	if opt.Format == "" {
		opt.Format = "epub"
	}
	if !ValidFormat(opt.Format) {
		return result, fmt.Errorf("format must be epub, azw3 or pdf")
	}
	if opt.OutputDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return result, err
		}
		opt.OutputDir = filepath.Join(home, "Downloads", "books")
	}
	var src source.Source
	for _, s := range a.Sources {
		switch {
		case strings.HasPrefix(id, "gutenberg:"):
			if _, ok := s.(interface{ IsGutenberg() }); ok {
				src = s
			}
		case strings.HasPrefix(id, "anna:"):
			if _, ok := s.(interface{ IsAnnasArchive() }); ok {
				src = s
			}
		case strings.HasPrefix(id, "standard:"):
			if _, ok := s.(interface{ IsStandardEbooks() }); ok {
				src = s
			}
		}
	}
	if src == nil {
		return result, fmt.Errorf("unknown source ID: use a numbered result or gutenberg:<id> / standard:<id>")
	}
	dl, err := src.Fetch(ctx, id, opt.Format)
	if err != nil {
		return result, err
	}
	if err = os.MkdirAll(opt.OutputDir, 0700); err != nil {
		return result, err
	}
	work, err := os.MkdirTemp(opt.OutputDir, ".bookget-")
	if err != nil {
		return result, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	input := filepath.Join(work, "download."+dl.Format)
	f, err := os.OpenFile(input, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	resp, err := dl.Client.Get(ctx, dl.URL)
	if err != nil {
		_ = f.Close()
		return result, err
	}
	if resp.ContentLength > safehttp.MaxDownload {
		_ = resp.Body.Close()
		_ = f.Close()
		return result, fmt.Errorf("download exceeds 200 MB")
	}
	err = safehttp.CopyLimited(f, resp.Body, safehttp.MaxDownload)
	_ = resp.Body.Close()
	closeErr := f.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	report, messages, err := a.check(ctx, input, dl.Format)
	result.Messages = append(result.Messages, messages...)
	if err != nil {
		return result, err
	}
	title, author := dl.Book.Title, dl.Book.Author
	if report.Title != "" {
		title = report.Title
	}
	if report.Author != "" {
		author = report.Author
	}
	output := input
	if dl.Format != opt.Format {
		convert := a.Convert
		if convert == nil {
			convert = Calibre
		}
		output = filepath.Join(work, "converted."+opt.Format)
		if err = convert(ctx, input, output); err != nil {
			return result, err
		}
		stat, e := os.Stat(output)
		if e != nil {
			return result, e
		}
		if stat.Size() > safehttp.MaxDownload {
			return result, fmt.Errorf("converted file exceeds 200 MB")
		}
		_, messages, err = a.check(ctx, output, opt.Format)
		result.Messages = append(result.Messages, messages...)
		if err != nil {
			return result, err
		}
	}
	final := filepath.Join(opt.OutputDir, Filename(title, author, opt.Format))
	if existing, statErr := os.Lstat(final); statErr == nil {
		same, e := sameContent(output, final, existing)
		if e != nil {
			return result, e
		}
		if !same {
			return result, fmt.Errorf("destination contains a different file; it was preserved: %s", final)
		}
		result.Messages = append(result.Messages, "Cópia local existente conferida; seguindo sem sobrescrevê-la.")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return result, statErr
	} else {
		// Publish only complete files, atomically and without overwriting a path.
		if err = os.Link(output, final); err != nil {
			return result, err
		}
	}
	result.Path = final
	if opt.Send {
		client := a.Ember
		if client == nil {
			client = ember.New()
		}
		if err = client.Send(ctx, final); errors.Is(err, ember.ErrNotLinked) {
			result.Messages = append(result.Messages, err.Error())
		} else if err != nil {
			return result, err
		} else {
			result.Sent = true
		}
	}
	return result, nil
}

func sameContent(checked, existing string, expected os.FileInfo) (bool, error) {
	if !expected.Mode().IsRegular() {
		return false, nil
	}
	original, err := os.Open(checked)
	if err != nil {
		return false, err
	}
	defer func() { _ = original.Close() }()
	target, err := os.Open(existing)
	if err != nil {
		return false, err
	}
	defer func() { _ = target.Close() }()
	a, err := original.Stat()
	if err != nil {
		return false, err
	}
	b, err := target.Stat()
	if err != nil {
		return false, err
	}
	if !os.SameFile(expected, b) || !b.Mode().IsRegular() || a.Size() != b.Size() || b.Size() > safehttp.MaxDownload {
		return false, nil
	}
	left, right := sha256.New(), sha256.New()
	if _, err = io.Copy(left, io.LimitReader(original, safehttp.MaxDownload+1)); err != nil {
		return false, err
	}
	if _, err = io.Copy(right, io.LimitReader(target, safehttp.MaxDownload+1)); err != nil {
		return false, err
	}
	return string(left.Sum(nil)) == string(right.Sum(nil)), nil
}
