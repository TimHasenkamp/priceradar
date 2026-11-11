"""PriceRadar fetcher sidecar.

Renders a URL in Scrapling's stealth browser (Camoufox) and returns the HTML.
It deliberately contains no shop logic: parsing happens in the Go backend.

API:
  POST /fetch   {"url": "...", "timeout_ms": 60000, "wait_selector": "css (optional)"}
                -> 200 {"status": int, "final_url": str, "html": str}
                -> 4xx/502 {"error": str}
  GET  /healthz -> 200 {"status": "ok"}

Requests are handled one at a time (plain HTTPServer, single thread), which
matches the backend's "never in parallel" policy.
"""

from __future__ import annotations

import json
import logging
import os
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse

from scrapling.fetchers import StealthyFetcher

log = logging.getLogger("fetcher")

ALLOWED_HOSTS = {
    h.strip().lower()
    for h in os.environ.get("FETCHER_ALLOWED_HOSTS", "www.otto.de,otto.de").split(",")
    if h.strip()
}
MAX_BODY = 64 * 1024


def fetch(url: str, timeout_ms: int, wait_selector: str | None) -> dict:
    kwargs = {}
    if wait_selector:
        # Bot-protection challenges (e.g. Kasada) are solved by JS in the
        # browser and then reload the page: wait until real content exists.
        kwargs = {"wait_selector": wait_selector, "wait_selector_state": "attached"}
    page = StealthyFetcher.fetch(url, headless=True, network_idle=True, timeout=timeout_ms, **kwargs)
    html = getattr(page, "html_content", None) or page.body
    if isinstance(html, bytes):
        html = html.decode("utf-8", "replace")
    return {"status": int(page.status), "final_url": str(getattr(page, "url", url)), "html": html}


class Handler(BaseHTTPRequestHandler):
    server_version = "priceradar-fetcher/0.1"

    def _send(self, code: int, payload: dict) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/healthz":
            self._send(200, {"status": "ok"})
        else:
            self._send(404, {"error": "not found"})

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/fetch":
            self._send(404, {"error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0 or length > MAX_BODY:
                raise ValueError("bad body length")
            req = json.loads(self.rfile.read(length))
            url = str(req["url"])
            timeout_ms = max(5_000, min(int(req.get("timeout_ms", 60_000)), 180_000))
            wait_selector = req.get("wait_selector") or None
            if wait_selector is not None and (not isinstance(wait_selector, str) or len(wait_selector) > 200):
                raise ValueError("wait_selector must be a string <= 200 chars")
        except (ValueError, KeyError, TypeError) as exc:
            self._send(400, {"error": f"invalid request: {exc}"})
            return

        parsed = urlparse(url)
        if parsed.scheme not in ("http", "https") or (parsed.hostname or "").lower() not in ALLOWED_HOSTS:
            self._send(400, {"error": f"host not allowed: {parsed.hostname}"})
            return

        started = time.monotonic()
        try:
            result = fetch(url, timeout_ms, wait_selector)
        except Exception as exc:  # browser/network failures -> transient for the backend
            log.warning("fetch failed url=%s err=%r", url, exc)
            self._send(502, {"error": f"{type(exc).__name__}: {exc}"})
            return
        log.info("fetched url=%s status=%s bytes=%d took=%.1fs", url, result["status"],
                 len(result["html"]), time.monotonic() - started)
        self._send(200, result)

    def log_message(self, fmt: str, *args) -> None:  # route access log through logging
        log.debug(fmt, *args)


def main() -> None:
    logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO").upper(),
                        format="%(asctime)s %(levelname)s %(name)s %(message)s")
    addr = os.environ.get("FETCHER_ADDR", "0.0.0.0")
    port = int(os.environ.get("FETCHER_PORT", "8000"))
    log.info("listening on %s:%d, allowed hosts=%s", addr, port, sorted(ALLOWED_HOSTS))
    HTTPServer((addr, port), Handler).serve_forever()


if __name__ == "__main__":
    main()
