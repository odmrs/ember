# Catalog publication validation

This records checks for the catalog integration and illustrated tutorial. It is
not a guarantee that no vulnerabilities remain. Tests use fake local services;
no automated test logs into a source or uploads to a physical Kindle.

## Executed checks

| Check | Result |
| --- | --- |
| Ember `go test -race ./...` | Passed |
| Helper `go test -race ./...` | Passed, including manifest-based markup regression |
| Ember and helper `go vet ./...` | Passed |
| Helper `golangci-lint run ./...` | Passed with v1.64.8 |
| `node --test internal/web/bookget_ui_test.cjs` | Six tests passed |
| Go compilation of Ember and helper | Passed |
| Go formatting and whitespace | Checked |
| Gitleaks directory scan, redacted output | No findings |
| Personal paths/internal project identifiers in publication text | No new matches; inherited private-key test marker contains no private credential |
| Screenshot rendering | Two PNGs generated from synthetic local APIs and inspected |
| Documentation destinations | Relative links and screenshot files checked |

## Runtime evidence at boundaries

- Catalog HTTP fakes assert title, EPUB and language query parameters. CLI tests
  exercise language flags both before/after the title and filter English results
  from a Portuguese search. UI tests separately check query construction.
- Catalog API tests cover invalid input, Host/Origin refusal, concurrent-operation
  rejection, errors, stable selection tokens across searches, expiry and progress.
- UI tests exercise search, choose, upload, offline saves, API failure recovery,
  repeated-submit suppression and untrusted metadata remaining plain text.
- Download HTTP fakes exercise allowed/refused redirects, size limits and origin
  policy. IP-policy tests classify private/reserved/public addresses without
  connecting to external systems.
- Crafted EPUB tests exercise traversal, absolute/Windows paths, bombs, symlinks,
  executable content, wrong magic/mimetype and active attributes/scripts.
- Review identified a filename-extension gap: manifest-declared XHTML/SVG could
  use another extension. Inspection now also follows OPF media types and resolves
  URI paths relative to each container rootfile. Regression tests cover `.dat`,
  extensionless SVG, escaped filenames, relative paths, safe renamed chapters and
  a second OPF rootfile. These tests passed after the correction.
- Fake VirusTotal checks exercise hash-only lookup, unknown files, detections,
  HTTP errors and malformed responses; fake Ember tests exercise local upload,
  offline state and refusal of remote origins/redirects.
- The separate Chrome transport is exercised with fake pipe responses, including
  mismatched IDs, cancellation and evaluation errors without leaking page text.

## Publication scope

Only the catalog source, integration changes, tests, tutorial, security model,
synthetic screenshots and screenshot renderer are included. Local installers,
execution reports, browser state, real downloads and binaries are excluded.
Secret scanning was supplemented by filename/path review and visual inspection;
it does not replace review of the exact committed tree.

## Limits and recovery

A source can change HTML or require human verification. Availability of the
ordinary slow-download route remains dependent on that source. Physical Kindle
receipt, its reader behavior and external source availability are not certified
by the automated checks. No new live download is asserted by this publication.

The browser fallback is currently implemented for macOS Chrome at its standard
application path. Tests do not certify this path on Windows/Linux. Legacy PDF
and AZW3 header checks provide less assurance than the EPUB policy. Parser and
reader vulnerabilities, embedded media and external references remain risks.

Rollback: stop Ember and keep the existing profile, then run binaries built from
the previous commit in a separate directory. Existing completed downloads are
preserved on normal failures. The tutorial explains manual browser-profile and
cache removal without deleting the SSH pairing profile or downloaded books.
