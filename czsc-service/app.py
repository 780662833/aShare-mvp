"""标准库 HTTP 包装层；默认只监听本机回环地址。"""

from __future__ import annotations

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import logging

from czsc_service.analyzer import analyze_payload

MAX_BODY_BYTES = 10 * 1024 * 1024
TOKEN = os.environ.get("CZSC_SERVICE_TOKEN", "").strip()


class Handler(BaseHTTPRequestHandler):
    server_version = "CZSCService/1.0"

    def _write_json(self, status: int, payload: dict) -> None:
        encoded = json.dumps(payload, ensure_ascii=False, allow_nan=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def do_GET(self) -> None:
        if self.path == "/healthz":
            self._write_json(200, {"status": "ok", "service": "czsc"})
            return
        self._write_json(404, {"error": "not_found"})

    def do_POST(self) -> None:
        if self.path != "/api/v1/analyze":
            self._write_json(404, {"error": "not_found"})
            return
        if TOKEN and self.headers.get("Authorization", "") != f"Bearer {TOKEN}":
            self._write_json(401, {"error": "unauthorized"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            self._write_json(400, {"error": "invalid_content_length"})
            return
        if length <= 0 or length > MAX_BODY_BYTES:
            self._write_json(413, {"error": "invalid_body_size"})
            return

        try:
            payload = json.loads(self.rfile.read(length).decode("utf-8"))
            result = analyze_payload(payload)
            self._write_json(200, result)
        except (UnicodeDecodeError, json.JSONDecodeError, ValueError) as exc:
            self._write_json(400, {"error": str(exc)})
        except Exception as exc:  # noqa: BLE001 - isolate Rust/PyO3 exceptions at HTTP boundary
            logging.exception("CZSC analysis failed")
            self._write_json(500, {"error": "analysis_failed", "detail": str(exc)})

    def log_message(self, fmt: str, *args) -> None:
        logging.info("%s - %s", self.address_string(), fmt % args)


def main() -> None:
    logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO"))
    host = os.environ.get("CZSC_SERVICE_HOST", "127.0.0.1")
    port = int(os.environ.get("CZSC_SERVICE_PORT", "8091"))
    if host not in {"127.0.0.1", "::1", "localhost"} and not TOKEN:
        raise SystemExit("非本机监听必须设置 CZSC_SERVICE_TOKEN")
    server = ThreadingHTTPServer((host, port), Handler)
    logging.info("CZSC service listening on %s:%d", host, port)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        logging.info("CZSC service stopping")
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
