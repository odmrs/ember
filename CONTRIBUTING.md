# Contributing to ember

Thanks for wanting to help. ember is a small project kept by one person, so the
most useful thing you can do is tell me clearly what is wrong or what you would
like changed. This page explains how, and what to expect.

## Issues first, code second

**Please open an issue before writing code.** A detailed issue is worth more to
this project than an unsolicited pull request, for three reasons:

- Most problems with ember depend on the Kindle, the firmware, the KOReader
  version and the network. I need those details to understand the bug, and an
  issue is where they get written down.
- A fix can be right in code and still be the wrong fix: it may add a
  dependency, reach outside `127.0.0.1`, or touch the Kindle in a way the
  project has decided against (see [`docs/adr/`](docs/adr/)). Agreeing on the
  approach first saves you from work that gets turned down.
- A good description lets anyone fix it, and it stays useful after the fix as a
  record of why the code is the way it is.

So the flow is:

1. Open an issue: a [bug report](#reporting-a-bug) or a
   [change request](#requesting-a-change).
2. We agree on what should happen.
3. Then, if you want, send the pull request and link the issue.

Pull requests without an agreed issue are fine only for small, obvious fixes:
typos, broken links, translation fixes, and new rows for the name-cleaner
corpus. Anything else will probably be closed with a request to open an issue
first. That is not a judgement of the code, just how the project is run.

Before opening an issue, search the existing ones, including closed ones. One
issue per problem: two bugs in one report usually means one of them gets lost.

## Reporting a bug

Use the **Bug report** form. It asks for:

- **Kindle model** and **firmware version** (Settings → Device Options → Device
  Info on the Kindle).
- **KOReader version** (KOReader → Help → About).
- **How the Kindle is reached**: KOReader's built-in SSH server, usbnetlite, or
  something else.
- **Your computer's OS** and version (macOS 15.3, Ubuntu 24.04, Windows 11...).
- **ember version or commit**: the footer of the dashboard shows the version,
  and `git rev-parse --short HEAD` gives the commit if you built from source.
- **Steps to reproduce**, starting from a fresh start of ember if you can.
- **What you expected** and **what happened instead**.
- **A screenshot** if anything looks wrong on screen.
- **The text of the LOG strip** at the bottom of the dashboard, copied as text.
- **The terminal output** of ember from start to the moment of the problem.

### Removing private details

You do not have to share anything private. Before pasting, feel free to:

- replace IP addresses with something like `192.168.x.x` (keep it consistent if
  several addresses matter, e.g. `KINDLE_IP` and `NEW_KINDLE_IP`);
- replace book titles and folder names with placeholders, as long as you keep
  what matters for the bug: the extension, the size, the length of the name, and
  any unusual characters (accents, emoji, `:`, very long names);
- leave out host key fingerprints. Never paste the contents of ember's config
  folder: it holds the private key ember uses to log in to your Kindle.

Say what you changed, so I do not chase a detail you edited.

### A good bug report and a bad one

Bad:

> Upload doesn't work.

There is nothing to act on: no device, no file, no idea where it stops or what
"doesn't work" looks like.

Good:

> **Upload stalls at 99% for a large PDF**
>
> Kindle Paperwhite 5 (11th gen), firmware 5.16.21, KOReader v2025.08 using its
> built-in SSH server. ember at commit `2870047`, macOS 15.3, both on the same
> home Wi-Fi.
>
> 1. Start ember, it reconnects to the paired Kindle.
> 2. Open `/mnt/us/documents/Manuals`.
> 3. Upload a 300 MB PDF (`Service Manual.pdf`) with + Upload.
>
> Expected: the upload finishes and the book appears in the list.
>
> Actual: progress reaches 99% and stays there. After about two minutes the
> queue shows an error. A 40 MB PDF uploads fine. The Kindle screen stayed on
> the whole time.
>
> LOG strip: `upload failed: Service Manual.pdf: connection lost`
>
> Terminal:
> ```
> 2026/10/04 21:12:03 ember 0.1.0-dev running at http://127.0.0.1:8737/
> 2026/10/04 21:15:47 upload Service Manual.pdf: sftp: write: connection lost
> ```
>
> Screenshot of the stuck queue attached. Over SSH I can see a leftover
> `.ember-<hex>.part` file in the folder afterwards.

It says which device and versions, what was done, what was expected, what
happened, how big the file was, what still works, and gives the exact log lines.

## Requesting a change

Use the **Change request** form for new features, changes to how something
works, and name-cleaner results you disagree with. Describe the problem you
have before the solution you have in mind: there may be a simpler way to solve
it, and the problem is what decides whether the change fits ember.

Include a concrete example: the input, what ember does today, and what you want
it to do instead.

### A good change request and a bad one

Bad:

> The cleaned names are ugly.

Ugly how, for which files? There is no way to know what to change, or to write
a test for it.

Good:

> **Clean names drops the series from ePubLibre names**
>
> I download from ePubLibre, whose names carry the series in brackets before
> the title. Input:
>
> `Sanderson, Brandon - [Mistborn 01] El imperio final (r1.2).epub`
>
> ember proposes today (preview, no title or author in the file's metadata):
>
> `El imperio final - Brandon Sanderson.epub`
>
> I would expect:
>
> `El imperio final (Mistborn 01) - Brandon Sanderson.epub`
>
> I keep about 40 books from several series in one folder, and without the
> series number I cannot tell the reading order from the file list. Rule 3 in
> `docs/research-noisy-names.md` keeps series in brackets for other sites, so
> I expected the same here.

It gives the exact input, today's output, the expected output and the reason,
which is everything needed to discuss the rule and turn it into a test.

## Easy contributions

These two kinds of pull request are welcome without an issue first.

### Translations

The dashboard strings live in `internal/web/static/i18n/`: `en.json` and
`pt.json`, one key per string.

- To fix a translation, edit the value in that language's catalog.
- To add or rename a key, change **every** catalog in the same pull request.
- To add a language, copy `en.json` to the new language code and translate all
  values; please mention it in the pull request so the language switcher can be
  wired up.

`go test ./internal/web/` checks that every catalog has the same keys
(`TestCatalogsHaveTheSameKeys`) and that every key the code uses exists, so run
it before sending.

### Name-cleaner corpus

The name cleaner (`internal/names`) is tested against real file names found in
the wild. If it cleans one of your files badly, or you have a real noisy name it
does not cover:

1. Add a row to the `corpus` table in `internal/names/corpus_test.go`: the name
   exactly as it is on disk, the name you expect, and the source constant
   (`SourcePattern`, `SourceGeneric`, or `""` for no change). A `want` equal to
   the input means "leave it alone".
2. Add the same name to the table in
   [`docs/research-noisy-names.md`](docs/research-noisy-names.md), with the
   language, the site family and where it was seen (a public link, or "the
   user's own library" for your own files).
3. Run `go test ./internal/names/`.

Real names only, copied character for character. Please do not invent names to
make a rule pass. If your expected output disagrees with a rule on that page,
open a change request instead and point to the rule.

## Development

You need Go 1.26 or newer. No Kindle is needed: the tests run against an
in-process fake Kindle (`internal/fakedevice`).

```sh
go test -race ./...
go vet ./...
gofmt -l .        # must print nothing
```

To run a copy of ember next to your real one, with its own profile and a free
port:

```sh
go run . -no-open -port 0 -config "$(mktemp -d)"
```

It prints the address it picked. A separate `-config` folder means your paired
Kindle and key are not touched.

### Project rules

- **No new dependency without an ADR.** ember has two direct third-party
  modules, pinned and vendored. Adding one needs a decision record in
  [`docs/adr/`](docs/adr/) saying why, and should be discussed in an issue first.
- **The dashboard makes no external requests.** No CDN, fonts, analytics or
  remote images. The Content Security Policy blocks them, and
  `TestNoExternalReferences` fails if any outside host appears in the static
  files.
- **Plain HTML, CSS and JavaScript.** No build step, no npm, no framework. The
  files in `internal/web/static/` are served as they are.
- **Code comments explain the code**, for someone reading it later. Not your
  process, not the history of the change.
- **Small, focused pull requests with tests.** One change per pull request. A
  bug fix comes with a test that fails without it.
- **Conventional commit titles**, lower case, with a scope when it helps:
  `fix(connector): ...`, `feat(web): ...`, `docs: ...`, `test(names): ...`.
  The body says why, if the title does not.

Design decisions and their reasons are in [`docs/adr/`](docs/adr/). Read the
ones that touch your change before proposing something that goes against them.

## Security issues

Do not open a public issue for a security problem. Report it privately through
GitHub's **Report a vulnerability** button on the Security tab of the
repository. See [SECURITY.md](SECURITY.md) for what counts and what happens next.
