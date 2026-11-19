// Package checker performs bounded local inspection before conversion or upload.
package checker

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
)

type Report struct {
	Title, Author string
	Findings      []string
}

var script = regexp.MustCompile(`(?i)<\s*(?:[a-z0-9_-]+:)?script\b`)

func Check(filename, format string) (Report, error) {
	var report Report
	f, err := os.Open(filename)
	if err != nil {
		return report, err
	}
	defer func() { _ = f.Close() }()
	var head [80]byte
	n, _ := f.Read(head[:])
	switch format {
	case "pdf":
		if !bytes.HasPrefix(head[:n], []byte("%PDF-")) {
			return report, fmt.Errorf("PDF magic mismatch")
		}
		return report, nil
	case "azw3":
		// Palm database BOOKMOBI header followed by a PalmDOC + MOBI record.
		if n < 80 || string(head[60:68]) != "BOOKMOBI" {
			return report, fmt.Errorf("AZW3 Palm database magic mismatch")
		}
		var offset int64
		var record [4]byte
		if _, err = f.ReadAt(record[:], 78); err != nil {
			return report, fmt.Errorf("invalid AZW3 record table: %w", err)
		}
		offset = int64(binary.BigEndian.Uint32(record[:]))
		var mobi [28]byte
		if _, err = f.ReadAt(mobi[:], offset+16); err != nil {
			return report, fmt.Errorf("invalid AZW3 record: %w", err)
		}
		if string(mobi[:4]) != "MOBI" || binary.BigEndian.Uint32(mobi[20:24]) != 8 {
			return report, fmt.Errorf("AZW3 requires a KF8 MOBI record")
		}
		return report, nil
	case "epub":
		if !bytes.HasPrefix(head[:n], []byte("PK\x03\x04")) {
			return report, fmt.Errorf("EPUB ZIP magic mismatch")
		}
		return epub(filename)
	default:
		return report, fmt.Errorf("unsupported format: %s", format)
	}
}
func epub(filename string) (Report, error) {
	var report Report
	z, err := zip.OpenReader(filename)
	if err != nil {
		return report, err
	}
	defer func() { _ = z.Close() }()
	stat, err := os.Stat(filename)
	if err != nil {
		return report, err
	}
	if len(z.File) > 10000 {
		return report, fmt.Errorf("EPUB contains too many ZIP entries")
	}
	limit := uint64(stat.Size()) * 10
	var total uint64
	bad := false
	files := map[string]*zip.File{}
	for _, f := range z.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		unsafe := strings.HasPrefix(name, "/") || strings.Contains(name, ":")
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				unsafe = true
			}
		}
		if unsafe {
			report.Findings = append(report.Findings, "path traversal: "+f.Name)
			bad = true
		}
		if _, exists := files[name]; exists {
			report.Findings = append(report.Findings, "duplicate ZIP entry: "+name)
			bad = true
		}
		files[name] = f
		if f.UncompressedSize64 > limit-total {
			report.Findings = append(report.Findings, "zip bomb: total uncompressed size exceeds 10x archive size")
			bad = true
			break
		}
		total += f.UncompressedSize64
		if f.Mode()&os.ModeSymlink != 0 {
			report.Findings = append(report.Findings, "symlink: "+f.Name)
		}
		switch strings.ToLower(path.Ext(name)) {
		case ".exe", ".dll", ".sh", ".js", ".com", ".bat", ".cmd", ".ps1", ".scr", ".msi", ".dylib", ".so":
			report.Findings = append(report.Findings, "executable content: "+f.Name)
		}
	}
	if bad {
		return report, fmt.Errorf("unsafe EPUB archive")
	}
	mime := files["mimetype"]
	if mime == nil {
		return report, fmt.Errorf("EPUB mimetype is missing")
	}
	data, err := read(mime, 128)
	if err != nil || string(data) != "application/epub+zip" {
		return report, fmt.Errorf("wrong EPUB mimetype")
	}
	container := files["META-INF/container.xml"]
	if container == nil {
		return report, fmt.Errorf("EPUB container is missing")
	}
	data, err = read(container, 1<<20)
	if err != nil {
		return report, err
	}
	var c struct {
		Files []struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err = xml.Unmarshal(data, &c); err != nil {
		return report, err
	}
	if len(c.Files) == 0 {
		return report, fmt.Errorf("EPUB has no OPF rootfile")
	}
	markup := map[string]bool{}
	for i, root := range c.Files {
		opf := files[root.Path]
		if opf == nil {
			return report, fmt.Errorf("EPUB OPF is missing")
		}
		data, err = read(opf, 4<<20)
		if err != nil {
			return report, err
		}
		var pkg struct {
			Title    string   `xml:"metadata>title"`
			Creators []string `xml:"metadata>creator"`
			Items    []struct {
				Href string `xml:"href,attr"`
				Type string `xml:"media-type,attr"`
			} `xml:"manifest>item"`
		}
		if err = xml.Unmarshal(data, &pkg); err != nil {
			return report, err
		}
		if i == 0 {
			report.Title = strings.TrimSpace(pkg.Title)
			report.Author = strings.TrimSpace(strings.Join(pkg.Creators, ", "))
		}
		for _, item := range pkg.Items {
			mediaType := strings.ToLower(strings.TrimSpace(strings.Split(item.Type, ";")[0]))
			if mediaType != "text/html" && mediaType != "application/xml" && mediaType != "text/xml" && !strings.HasSuffix(mediaType, "+xml") {
				continue
			}
			href, e := url.Parse(item.Href)
			if e != nil {
				return report, fmt.Errorf("invalid EPUB manifest href")
			}
			// Remote resources are not fetched by the checker. Local manifest paths
			// use decoded URI paths relative to each OPF, regardless of extension.
			if href.IsAbs() || href.Host != "" {
				continue
			}
			name := path.Clean(path.Join(path.Dir(root.Path), href.Path))
			if files[name] == nil {
				return report, fmt.Errorf("EPUB manifest markup is missing: %s", name)
			}
			markup[name] = true
		}
	}
	// Read every entry to validate actual sizes and CRC, without extracting paths.
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return report, err
		}
		_, err = io.Copy(io.Discard, r)
		_ = r.Close()
		if err != nil {
			return report, fmt.Errorf("invalid ZIP entry %s: %w", f.Name, err)
		}
		ext := strings.ToLower(path.Ext(f.Name))
		if markup[strings.ReplaceAll(f.Name, "\\", "/")] || ext == ".xhtml" || ext == ".html" || ext == ".htm" || ext == ".svg" {
			data, err := read(f, 16<<20)
			if err != nil {
				return report, err
			}
			if err = inspectXML(data); err != nil {
				report.Findings = append(report.Findings, "unsafe XHTML: "+f.Name+": "+err.Error())
			}
			if script.Match(data) {
				report.Findings = append(report.Findings, "script in XHTML: "+f.Name)
			}
		}
	}
	// Active content and symlinks are reported and blocked before invoking Calibre.
	if len(report.Findings) > 0 {
		return report, fmt.Errorf("EPUB contains unsafe content")
	}
	return report, nil
}
func read(f *zip.File, limit int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("entry %s exceeds inspection limit", f.Name)
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(io.LimitReader(r, limit+1))
}

func inspectXML(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invalid XML: %w", err)
		}
		el, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if strings.EqualFold(el.Name.Local, "script") {
			return fmt.Errorf("script element")
		}
		for _, attr := range el.Attr {
			key := strings.ToLower(attr.Name.Local)
			value := strings.ToLower(strings.TrimSpace(attr.Value))
			if strings.HasPrefix(key, "on") || strings.HasPrefix(value, "javascript:") || strings.HasPrefix(value, "vbscript:") {
				return fmt.Errorf("active attribute %s", key)
			}
		}
	}
}
