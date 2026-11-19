#!/usr/bin/env python3
"""Render real catalog assets against fake loopback APIs; no accounts or Kindle.

Requires macOS Chrome already installed. Outputs only synthetic documentation
screenshots. Never connects to a live Ember or a remote catalog.
"""
import argparse
import functools
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parent.parent
STATIC = ROOT / "internal/web/static"
CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
BOOK = {
    "ID": "anna:" + "0" * 32,
    "Title": "Dom Casmurro",
    "Author": "Machado de Assis",
    "Year": "1899",
    "Source": "Anna's Archive",
    "Publisher": "Edição de demonstração",
    "Details": "Português [pt] · EPUB · metadados fictícios para documentação",
    "Formats": {"epub": "https://example.invalid/demo"},
    "token": "demo-token-never-valid-in-ember",
}
DEMO = r"""
window.addEventListener('load', async () => {
  try {
    await setLang('pt');
    const banner = document.createElement('p');
    banner.textContent = 'DEMONSTRAÇÃO • dados fictícios • sem conta ou Kindle conectado';
    banner.style.cssText = 'margin:0 0 18px;padding:12px;border:1px solid #0071b8;color:#0071b8;font:14px monospace';
    document.body.prepend(banner);
    document.querySelector('.book-catalog').scrollIntoView();
    document.getElementById('catalog-query').value = 'Dom Casmurro';
    document.getElementById('catalog-language').value = 'pt';
    document.getElementById('catalog-search').requestSubmit();
    if (new URLSearchParams(location.search).get('scene') === 'saved') {
      const timer = setInterval(() => {
        const button = document.querySelector('#catalog-results button');
        if (button && !button.disabled) { clearInterval(timer); button.click(); }
      }, 50);
    }
  } catch (error) { document.title = 'Screenshot setup failed'; }
});
"""


class Handler(SimpleHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def json(self, data):
        body = json.dumps(data, ensure_ascii=False).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = urlsplit(self.path).path
        if path == "/api/status":
            return self.json({"state": "unpaired", "version": "demo"})
        if path == "/api/pair":
            return self.json({"phase": "idle", "candidates": []})
        if path == "/api/books/status":
            return self.json({"busy": False, "message": ""})
        if path == "/api/books/search":
            return self.json({"books": [BOOK], "warnings": ""})
        if path == "/api/events":
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            self.wfile.write(b'event: state\ndata: {"phase":"idle","online":true}\n\n')
            return
        if path in ("/", "/index.html", "/demo.js"):
            data = DEMO.encode() if path == "/demo.js" else (STATIC / "index.html").read_bytes().replace(
                b"</head>", b'<script src="demo.js" defer></script></head>')
            self.send_response(200)
            self.send_header("Content-Type", "text/javascript; charset=utf-8" if path == "/demo.js" else "text/html; charset=utf-8")
            self.send_header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:")
            self.end_headers()
            self.wfile.write(data)
            return
        if path.startswith("/api/"):
            self.send_error(404)
            return
        # Serve static UI files only, never project/configuration directories.
        return super().do_GET()

    def do_POST(self):
        if urlsplit(self.path).path != "/api/books/get":
            self.send_error(405)
            return
        self.rfile.read(min(int(self.headers.get("Content-Length", "0")), 4096))
        self.json({"path": "~/Downloads/books/Dom Casmurro - Machado de Assis.epub",
                   "sent": False, "messages": ["Kindle desconectado — cópia local preservada (demonstração)"]})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--chrome", default=CHROME)
    args = parser.parse_args()
    if not Path(args.chrome).is_file():
        parser.error("Chrome is required; nothing will be installed automatically")
    output = ROOT / "docs/screenshots"
    output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(Handler, directory=str(STATIC)))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        for scene, name in [("search", "catalog-language.png"), ("saved", "catalog-saved.png")]:
            with tempfile.TemporaryDirectory(prefix="ember-docs-chrome-") as profile:
                command = [args.chrome, "--headless=new", "--no-first-run", "--no-default-browser-check",
                           "--disable-background-networking", "--disable-sync", "--metrics-recording-only",
                           "--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1",
                           "--user-data-dir=" + profile, "--window-size=1360,1000", "--hide-scrollbars",
                           "--virtual-time-budget=5000", "--screenshot=" + str(output / name),
                           f"http://127.0.0.1:{server.server_port}/?scene={scene}"]
                if os.uname().machine == "arm64":
                    command = ["/usr/bin/arch", "-arm64"] + command
                subprocess.run(command, check=True, timeout=45, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                               env={key: os.environ[key] for key in ("HOME", "PATH", "TMPDIR", "LANG") if key in os.environ})
                print(output.relative_to(ROOT) / name)
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


if __name__ == "__main__":
    main()
