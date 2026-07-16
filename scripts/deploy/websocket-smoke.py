#!/usr/bin/env python3
"""Perform a bounded RFC 6455 HTTP upgrade against an APEX Load endpoint."""

from __future__ import annotations

import argparse
import base64
import hashlib
import os
import socket
import ssl
import urllib.parse


WEBSOCKET_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
MAX_HEADER_BYTES = 16 * 1024


def read_http_headers(connection: socket.socket) -> bytes:
    response = bytearray()
    while b"\r\n\r\n" not in response:
        chunk = connection.recv(4096)
        if not chunk:
            raise SystemExit("websocket smoke: connection closed before the upgrade response completed")
        response.extend(chunk)
        if len(response) > MAX_HEADER_BYTES:
            raise SystemExit("websocket smoke: upgrade response headers exceed the safety limit")
    return bytes(response).split(b"\r\n\r\n", 1)[0]


def validate_upgrade(response: bytes, key: str) -> None:
    try:
        lines = response.decode("iso-8859-1").split("\r\n")
    except UnicodeDecodeError as error:
        raise SystemExit("websocket smoke: upgrade response headers are invalid") from error
    status_parts = lines[0].split()
    if len(status_parts) < 2 or not status_parts[0].startswith("HTTP/") or status_parts[1] != "101":
        raise SystemExit(f"websocket smoke: upgrade failed: {lines[0]}")

    headers: dict[str, list[str]] = {}
    for line in lines[1:]:
        if ":" not in line:
            raise SystemExit("websocket smoke: malformed upgrade response header")
        name, value = line.split(":", 1)
        headers.setdefault(name.strip().lower(), []).append(value.strip())

    def tokens(name: str) -> set[str]:
        return {
            token.strip().lower()
            for value in headers.get(name, [])
            for token in value.split(",")
            if token.strip()
        }

    if "websocket" not in tokens("upgrade"):
        raise SystemExit("websocket smoke: response is missing Upgrade: websocket")
    if "upgrade" not in tokens("connection"):
        raise SystemExit("websocket smoke: response is missing Connection: Upgrade")
    accepts = headers.get("sec-websocket-accept", [])
    expected = base64.b64encode(hashlib.sha1((key + WEBSOCKET_GUID).encode("ascii")).digest()).decode("ascii")
    if len(accepts) != 1 or accepts[0] != expected:
        raise SystemExit("websocket smoke: Sec-WebSocket-Accept does not authenticate the request key")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("url")
    parser.add_argument("--token", default="")
    args = parser.parse_args()
    parsed = urllib.parse.urlparse(args.url)
    if parsed.scheme not in {"ws", "wss"} or not parsed.hostname:
        raise SystemExit("websocket smoke: URL must use ws:// or wss://")
    port = parsed.port or (443 if parsed.scheme == "wss" else 80)
    connection = socket.create_connection((parsed.hostname, port), timeout=10)
    connection.settimeout(10)
    if parsed.scheme == "wss":
        context = ssl.create_default_context()
        connection = context.wrap_socket(connection, server_hostname=parsed.hostname)
    key = base64.b64encode(os.urandom(16)).decode("ascii")
    path = parsed.path or "/"
    if parsed.query:
        path += "?" + parsed.query
    headers = [
        f"GET {path} HTTP/1.1",
        f"Host: {parsed.netloc}",
        "Upgrade: websocket",
        "Connection: Upgrade",
        f"Sec-WebSocket-Key: {key}",
        "Sec-WebSocket-Version: 13",
    ]
    if args.token:
        headers.append(f"Authorization: Bearer {args.token}")
    try:
        connection.sendall(("\r\n".join(headers) + "\r\n\r\n").encode("ascii"))
        response = read_http_headers(connection)
        validate_upgrade(response, key)
    finally:
        connection.close()
    print("RFC 6455 websocket upgrade verified")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
