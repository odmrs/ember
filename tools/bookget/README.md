# bookget

Go CLI and ember dashboard integration for finding existing EPUB editions in
Anna's Archive, checking downloaded files locally, and uploading through ember.
The default flow never converts a file and does not need Calibre.

```sh
go build -o bin/bookget ./cmd/bookget
./bin/bookget search "dom casmurro" --language pt
./bin/bookget get 1 --format epub
./bin/bookget get 1 --send
```

Searches return numbered records and stable `anna:<md5>` IDs. They preserve distinct
editions of the same title and show author, publisher, year and format. Exact
normalized title/author matches are kept ahead of unrelated upstream suggestions.
Use `--json` for integration; stdout contains JSON and stderr contains diagnostics.
The latest CLI search replaces numbering; integrations should retain full IDs.

The CLI searches all languages unless `--language pt` (or another two/three-letter
code) is supplied. The dashboard defaults to Portuguese. Filtering applies both
to the upstream query and to returned language metadata. Unknown-language records
are excluded when filtering; no language is inferred from the title.

See the [illustrated Portuguese tutorial](../../docs/bookget.pt-BR.md) and
[security model](../../docs/bookget-security.md). The browser fallback currently
requires the standard Google Chrome application on macOS.

## Account setup

On macOS, bookget reads the account key from Keychain: service
`bookget.annas-archive`, account `bookget`. It is never placed in process arguments,
source code or logs. `ANNAS_ARCHIVE_KEY` can explicitly override it. The key is an
account credential; having a registered account does not imply paid membership.

`BOOKGET_ANNA_URL` selects an exact Anna's Archive HTTPS origin. Otherwise,
`~/Library/Application Support/bookget/config.json` can provide `anna_url`; if no
origin is configured, bookget discovers the domains from Wikipedia's infobox.

An ordinary authenticated HTTP search is attempted first. If the site blocks it,
bookget launches a visible Google Chrome window with a separate private profile
under `~/Library/Application Support/bookget/browser`. It controls that one browser
through inherited DevTools pipes: no open debugging port, personal browser profile,
stealth flags, or disabled browser security. The profile retains its own session.
A site CAPTCHA must be completed by the user; bookget does not solve it.
Authenticated searches have a four-minute deadline including browser startup and manual verification.

## Download and send

Selecting a record follows its normal free slow-download link in the dedicated
browser. The site's waiting period and any manual verification still apply; the
resolver waits up to eight minutes. When a normal “Download now” link becomes
available, the CLI streams the file into a private temporary directory. It never
selects a paid fast-download link or bypasses a membership requirement. Availability
of a free download depends on the specific record and the upstream service.

Downloads use HTTPS, exact source-provided origins, validated public DNS addresses,
and a 200 MiB cap. Redirects to other domains are refused. Only cookies applicable
to the chosen file's origin are transferred from the dedicated profile, in memory.
A failed download is cleaned up. Completed files are published atomically without
overwriting existing files, using a hard link on the output filesystem.

Local checks run before publication or sending: ZIP magic and exact EPUB mimetype,
CRC integrity, container and OPF, traversal/absolute paths, duplicate entries,
symlinks, executable extensions, scripts and active XHTML/SVG attributes. Archives
above 10 times their compressed size or 10,000 entries are rejected. Every finding
blocks the file. The OPF title/creator determines `Title - Author.epub`; fallback
metadata is sanitized. Output defaults to `~/Downloads/books`; use `--out DIR`.

Optional `VT_API_KEY` enables a SHA-256 lookup only. No file upload or rescan is
performed. “Unknown to VT” is neutral; malicious detections or service errors block
publication. Structural checks and zero antivirus detections do not prove safety.

`--send` uploads through ember at `http://127.0.0.1:8737`; the dashboard can select
its own loopback port through `BOOKGET_EMBER_URL`. If the Kindle is unavailable,
the verified local copy is kept and reported. No existing Kindle file is replaced.
EPUB requires KOReader in this direct-upload workflow.

## Embed and verify

The Go library exposes `source.Source` (`Search`, `Fetch`) and `bookget.App.Get`.
`annasarchive` is the default CLI provider. Legacy Gutenberg and Standard Ebooks
adapters remain available to library callers; only those legacy adapters support
the earlier optional Calibre conversion path. Neither is used by the default CLI.
The only third-party Go module is the pinned Go HTML parser `golang.org/x/net`.
Google Chrome is an optional system dependency for browser-based operations; the
program does not install browsers, Calibre or other system programs automatically.

```sh
go test -race ./...
go vet ./...
golangci-lint run ./...
test -z "$(gofmt -l .)"
```

Automated tests use local fake sources, VirusTotal, ember and browser-pipe messages;
no test contacts the internet or the physical Kindle. External-source checks are manual and are not part of the automated suite. Source access does not
establish rights to a particular ebook; use material you are authorized to obtain.
