# Security policy

## Supported versions

ember has no releases yet. Only the latest commit on `main` is supported, and
fixes land there.

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's private vulnerability
reporting instead: go to the repository's **Security** tab and click **Report a
vulnerability**. Only the maintainer sees the report.

Include what you found, the steps or a proof of concept, the commit you tested,
and what an attacker could do with it. I aim to answer within a week.
Once a fix is on `main`, the advisory is published with credit to you, unless
you would rather stay anonymous.

## Scope

ember serves a dashboard on `127.0.0.1` and holds an SSH key that logs in to a
Kindle as root, so these are in scope:

- Anything that lets another website or another device on the network drive
  ember: cross-site requests, DNS rebinding, a forged `Host` or `Origin` that
  gets through, the dashboard listening on anything but `127.0.0.1`.
- Anything that lets someone reach the Kindle through ember, or makes ember
  connect to a host whose key does not match the pinned one.
- Path escapes: reading, writing, renaming or deleting anything outside the
  library root on the Kindle, through crafted file names, uploads or API
  calls.
- Exposure of ember's private key or profile, in responses, logs or files with
  loose permissions.
- The dashboard loading anything from outside the local server.

Out of scope: problems that need someone who already controls your computer
account or the Kindle, the risks of KOReader's own "Login without password"
option (ember warns about it), and bugs in Go or the vendored modules that
ember does not make worse. Report those upstream.
