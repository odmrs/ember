package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"bookget"
	"bookget/annasarchive"
	"bookget/source"
	"bookget/virustotal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "bookget:", printable(err.Error()))
		os.Exit(1)
	}
}
func cachePath() (string, error) {
	dir := os.Getenv("BOOKGET_CACHE_DIR")
	if dir == "" {
		var err error
		dir, err = os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(dir, "bookget")
	}
	return filepath.Join(dir, "search.json"), nil
}
func saveCache(books []source.Book) error {
	p, err := cachePath()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(books)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".search-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), p)
}
func resolveID(value string) (string, error) {
	if n, err := strconv.Atoi(value); err == nil {
		p, e := cachePath()
		if e != nil {
			return "", e
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return "", fmt.Errorf("run bookget search first: %w", e)
		}
		var books []source.Book
		if e = json.Unmarshal(data, &books); e != nil {
			return "", e
		}
		if n < 1 || n > len(books) {
			return "", fmt.Errorf("result number outside 1..%d", len(books))
		}
		return books[n-1].ID, nil
	}
	return value, nil
}
func run(ctx context.Context, args []string, out, logs io.Writer) error {
	anna := annasarchive.New()
	anna.Progress = func(message string) { fmt.Fprintln(logs, message) }
	sources := []source.Source{anna}
	return runWithSources(ctx, args, out, logs, sources)
}
func runWithSources(ctx context.Context, args []string, out, logs io.Writer, sources []source.Source) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: bookget search <title> [--language pt|en|es|...] [--json] | get <n|id> [--format epub|azw3|pdf] [--send] [--out directory] [--json]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(logs)
	asJSON := flags.Bool("json", false, "machine-readable output")
	language := flags.String("language", "", "ebook language code, e.g. pt, en or es (search only)")
	format := flags.String("format", "epub", "epub, azw3 or pdf")
	send := flags.Bool("send", false, "send to ember")
	output := flags.String("out", "", "output directory")
	// Accept flags before or after the positional argument.
	var opts, positionals []string
	for i := 1; i < len(args); i++ {
		v := args[i]
		if strings.HasPrefix(v, "-") {
			opts = append(opts, v)
			if (v == "--format" || v == "-format" || v == "--out" || v == "-out" || v == "--language" || v == "-language") && i+1 < len(args) {
				i++
				opts = append(opts, args[i])
			}
		} else {
			positionals = append(positionals, v)
		}
	}
	if err := flags.Parse(opts); err != nil {
		return err
	}
	if len(positionals) == 0 {
		return fmt.Errorf("missing title or result ID")
	}
	if *language != "" && (len(*language) != 2 && len(*language) != 3 || strings.Trim(*language, "abcdefghijklmnopqrstuvwxyz") != "") {
		return fmt.Errorf("language must be a two or three letter lowercase code")
	}
	if *language != "" && args[0] != "search" {
		return fmt.Errorf("--language is only supported for search")
	}
	switch args[0] {
	case "search":
		for _, s := range sources {
			if configurable, ok := s.(interface{ SetLanguage(string) }); ok {
				configurable.SetLanguage(*language)
			} else if *language != "" {
				return fmt.Errorf("source does not support language filtering")
			}
		}
		books, errs := source.SearchAll(ctx, sources, strings.Join(positionals, " "))
		for _, err := range errs {
			fmt.Fprintln(logs, "warning:", printable(err.Error()))
		}
		if len(books) == 0 && len(errs) == len(sources) {
			return fmt.Errorf("all sources failed")
		}
		if err := saveCache(books); err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(books)
		}
		for i, b := range books {
			formats := make([]string, 0, len(b.Formats))
			for f := range b.Formats {
				formats = append(formats, f)
			}
			sort.Strings(formats)
			year := b.Year
			if year == "" {
				year = "year unknown"
			}
			fmt.Fprintf(out, "%d. %s — %s | %s | %s | %s [%s]\n", i+1, printable(b.Title), printable(b.Author), year, b.Source, strings.Join(formats, ", "), b.ID)
		}
		if len(books) == 0 {
			fmt.Fprintln(out, "No EPUB records found.")
		}
		return nil
	case "get":
		if len(positionals) != 1 {
			return fmt.Errorf("get needs one result number or ID")
		}
		id, err := resolveID(positionals[0])
		if err != nil {
			return err
		}
		app := bookget.App{Sources: sources}
		if key := os.Getenv("VT_API_KEY"); key != "" {
			app.VT = virustotal.New(key)
		}
		result, err := app.Get(ctx, id, bookget.Options{Format: *format, OutputDir: *output, Send: *send})
		for _, m := range result.Messages {
			fmt.Fprintln(logs, printable(m))
		}
		if err != nil {
			if result.Path != "" {
				return fmt.Errorf("%w; saved locally: %s", err, result.Path)
			}
			return err
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(result)
		}
		fmt.Fprintln(out, "Saved:", result.Path)
		if result.Sent {
			fmt.Fprintln(out, "Uploaded to Kindle through ember.")
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
