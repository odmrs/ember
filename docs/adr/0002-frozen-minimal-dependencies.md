# 2. Frozen, minimal dependencies

ember is privacy and security focused, so the supply chain is part of the threat
model. The only third-party module is `golang.org/x/crypto` (SSH), maintained by the
Go team, pinned in `go.mod` and vendored so builds are offline and reproducible and
nothing updates on its own. The frontend is plain HTML/CSS/JS embedded in the binary:
no Node toolchain, no npm packages, no CDN, no web fonts. Any new dependency needs an
ADR.
