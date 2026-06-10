#!/usr/bin/env python3

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit
import json
import time


ORDERS = []


class Handler(BaseHTTPRequestHandler):
    def _write(self, status, body, content_type="application/json"):
        payload = body if isinstance(body, (bytes, bytearray)) else json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self):
        path = urlsplit(self.path).path
        if path == "/":
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(
                b"""
<!doctype html><html><body>
  <h1>Synthetic App</h1>
  <a id="next-link" href="/next">Go to next</a>
</body></html>
"""
            )
            return

        if path == "/next":
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(
                b"<html><body><h1>Next page</h1></body></html>"
            )
            return

        if path == "/api/health":
            self._write(200, {"ok": True, "ts": time.time()})
            return

        if path == "/api/orders":
            self._write(200, {"orders": ORDERS, "count": len(ORDERS)})
            return

        self._write(404, {"error": "not found"})

    def do_POST(self):
        path = urlsplit(self.path).path
        if path == "/api/orders":
            length = int(self.headers.get("Content-Length", "0") or 0)
            raw = self.rfile.read(length).decode(errors="replace") if length > 0 else "{}"
            try:
                body = json.loads(raw)
            except json.JSONDecodeError:
                body = {"raw": raw}
            order = {
                "id": len(ORDERS) + 1,
                "payload": body,
                "received_at": time.time(),
            }
            ORDERS.append(order)
            self._write(200, {"status": "stored", "id": order["id"]})
            return

        self._write(404, {"error": "not found"})


def main():
    server = ThreadingHTTPServer(("0.0.0.0", 18080), Handler)
    print("Synthetic dummy server listening on http://127.0.0.1:18080")
    server.serve_forever()


if __name__ == "__main__":
    main()
