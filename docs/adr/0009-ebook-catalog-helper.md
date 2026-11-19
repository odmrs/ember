# 9. Catalog search and verified EPUB acquisition run in a separate helper

The dashboard delegates catalog operations to the local `bookget` executable,
without a shell. It passes server-issued stable record IDs, enforces concurrency,
time and output limits, and retains ember's existing origin/host guards. The
helper has its own Go module under `tools/bookget`; ember's vendored SSH module
and dependency graph are unchanged.

The default provider is Anna's Archive. Its HTML requires a real HTML parser,
so the helper pins the Go project's `golang.org/x/net` parser module. Account
keys are kept in macOS Keychain (or explicitly supplied through an environment
variable), not in browser settings sent to ember or in repository files.

When HTTP requests are challenged, the helper can launch a visible Chrome with
its own private profile and inherited DevTools pipes. It opens no debugging TCP
port and does not use the personal Chrome/Safari profile. Normal site waits and
manual CAPTCHA requirements remain. The default flow acquires existing EPUBs;
it does not convert them or select paid fast-download links.

A source-provided file URL is constrained to HTTPS and its exact origin, with
public addresses checked at connection time. The helper checks the complete
local file before calling ember's existing upload API. An unlinked Kindle leaves
the verified local copy in place. The helper is bundled beside the macOS ember
binary so the dashboard works without a shell PATH configuration.
