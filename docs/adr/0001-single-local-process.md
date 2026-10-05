# 1. One local process serves API and dashboard

Browsers cannot speak SSH, so a local backend is unavoidable. We ship it as one Go
program that serves both the JSON API and the static dashboard, bound to
`127.0.0.1` only, and opens the default browser on start. No daemon, no installer:
when the process exits, ember is gone.
