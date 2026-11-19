# Catalog security model

The optional `bookget` helper retrieves untrusted ebooks. Checks reduce exposure;
they do not certify a file as safe. Source metadata and download links are not
independent evidence of authenticity or of a right to obtain a book.

## Boundaries and controls

| Boundary | Control | Residual risk |
| --- | --- | --- |
| Website → local dashboard | Existing loopback listener, Host/Origin guards, same-origin assets and CSP | Another process with the user's privileges can access the local service. Do not forward its port. |
| Catalog text → browser UI | Metadata uses `textContent`, never untrusted HTML | Source text can still be misleading. |
| UI → helper process | `exec.CommandContext` with an argument vector; no shell; bounded query, language, output, concurrency and execution time | The trusted binary is selected locally via sibling path, `BOOKGET_BIN` or PATH; protect those paths. |
| Selection → acquisition | Random opaque tokens, 30-minute expiry, stable catalog ID retained server-side | Tokens are capability-like local session data, not a user-authentication system. |
| Account key → source | Keychain lookup on macOS or explicit environment override; form authentication; credential omitted from argv/log messages | Destination necessarily receives the key; local account compromise remains in scope for the user to manage. |
| Source → browser | Separate profile, normal browser security and site verification, inherited DevTools pipes; reduced child environment | Source scripts execute in Chrome. Profile isolation is not OS process sandboxing beyond Chrome's normal sandbox. |
| Source link → file download | HTTPS, exact allowed hostname, no URL credentials, no cross-origin redirects; public DNS checks at dial time for browser-resolved downloads | A public server can serve malicious bytes. The source chooses the initial external download host. |
| File bytes → publication | 200 MiB cap, private temporary directory, EPUB structural/content checks, optional VT SHA-256 lookup | Checks are not a complete EPUB validator, sanitizer or malware scanner. |
| Publication → filesystem | Sanitized metadata name, atomic hard-link publication, no overwriting; identical existing regular files compared by SHA-256 | The output filesystem must support hard links. A user-controlled directory is not safe against a hostile local account with equivalent access. |
| File → Kindle | Checks complete before upload; helper accepts IPv4 loopback Ember origin only and refuses redirects; Ember pins SSH host key and confines library paths | Root SSH access on KOReader remains privileged; reader/parser vulnerabilities are not eliminated. |

The ordinary HTTP source adapters use their own explicit HTTPS host lists. The
browser-resolved download path additionally uses `safehttp.NewPublic`, which
validates DNS addresses before connecting to the resolved IP. It does not rely
on a preflight DNS lookup followed by an unchecked second lookup.

## EPUB policy

The helper reads archive entries without extracting their paths. It rejects:

- Wrong magic or mimetype; broken archive/CRC; missing container or OPF.
- Absolute or traversal paths (including backslash normalization), duplicate
  names, symlinks and known executable extensions.
- More than 10,000 entries or declared uncompressed bytes above 10× archive size.
- Script elements or known event/JavaScript attributes in inspected XHTML, HTML
  and SVG; malformed XML and files beyond per-entry inspection limits.

Any reported finding blocks publication. EPUB compression and document-size
limits are deliberately conservative and can reject legitimate books. The
checker does not strip content and call the result safe. It does not inspect
all embedded media, fonts, stylesheet behavior, external references or future
reader features. It cannot rule out vulnerabilities in Chrome, Go libraries,
KOReader or a device firmware parser. PDF/AZW3 checks in legacy library paths
are header checks and provide substantially less assurance.

VirusTotal integration sends a SHA-256 identifier and API authentication, never
the ebook. A 404/unknown response is neutral. At least one malicious detection or
an API/transport error blocks publication when the integration is configured.
A hash-only lookup may reveal that a user is querying a particular known file.

## Privacy and publication rules

The public distribution contains source, synthetic test fixtures and demo
screenshots. It excludes personal account keys, Keychain exports, browser
profiles, cookies, books, local search/download logs, personal absolute paths,
installation reports, machine-specific installer scripts and executables.

Search caches contain titles and URLs. Browser profiles hold authenticated
sessions. Account responses, signed download URLs, API keys, SSH profiles and
logs should be treated as private even if a secret scanner reports no matches.
Secret scanning is a supplementary control, not proof that a file is harmless.

The screenshots are rendered from the real static assets with fake local API
responses and a fresh temporary Chrome profile. They do not contact a catalog,
log into an account, contact a Kindle or show an actual delivery. Their visible
demonstration banner is intentional. Existing screenshots elsewhere in the
repository predate this catalog change.

## Verification and operational limits

Automated tests use local `httptest` servers, crafted ZIP files and fake browser
pipe replies. They exercise hostile paths, archive bombs, script detection,
redirect and origin policies, account rejection, language handling, local-output
collisions and upload failures. UI tests verify that untrusted metadata remains
text. See [the validation record](bookget-validation.md) for executed commands.

Live source availability and physical Kindle delivery are separate checks and
are not established by a simulated successful upload. The source can change
markup, require a manual challenge, impose a queue or disappear. There is no
CAPTCHA solver, membership bypass or promise of access to every record.

Cancel normal operations and wait for cleanup before restarting. A forced
process kill or power failure may leave private `.bookget-*` staging directories;
inspect them with Ember stopped before removing them. Existing completed books
are preserved on failures. [The tutorial](bookget.pt-BR.md) covers rollback,
credential removal and practical troubleshooting.

Report vulnerabilities privately using [SECURITY.md](../SECURITY.md). A zero-risk
or “no vulnerabilities” guarantee is not supported by these tests.
