<!--
Pull requests need an issue the maintainer has agreed to, except tiny obvious
fixes (typos, translations, name-cleaner corpus rows). See CONTRIBUTING.md.
-->

Closes #

## What and why

<!-- What this changes and why. Point to the issue for the background. -->

## Tests

<!-- Which tests you added or changed, and what you ran. -->

- [ ] `go test -race ./...`
- [ ] `go vet ./...`
- [ ] `gofmt -l .` prints nothing

## Screenshots

<!-- For any dashboard change: before and after, light and dark theme if colours changed. Delete if not UI. -->

## Checklist

- [ ] One focused change, linked to an agreed issue (or a typo, translation or corpus fix).
- [ ] A bug fix comes with a test that fails without it.
- [ ] No new dependency, or an ADR in `docs/adr/` was added and agreed.
- [ ] The dashboard still makes no external requests; no build step added.
- [ ] Every i18n key added or renamed is in all catalogs.
- [ ] Commit titles follow conventional commits (`fix(connector): ...`).
- [ ] Tried on a real Kindle (say which below), or not needed for this change.
