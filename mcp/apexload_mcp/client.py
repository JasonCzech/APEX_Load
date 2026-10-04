from __future__ import annotations

import json
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from urllib.parse import quote, urlencode, urlparse

import httpx

DEFAULT_API_URL = "http://localhost:8080/api/v1"


JsonMap = dict[str, Any]


@dataclass(frozen=True)
class APEXLoadClientConfig:
    base_url: str = DEFAULT_API_URL
    api_key: str = ""
    bearer_token: str = ""
    default_project_id: str = ""
    timeout_seconds: float = 30.0

    @classmethod
    def from_env(cls) -> "APEXLoadClientConfig":
        timeout_raw = os.environ.get("APEXLOAD_TIMEOUT_SECONDS", "30").strip()
        try:
            timeout_seconds = float(timeout_raw)
        except ValueError:
            timeout_seconds = 30.0
        return cls(
            base_url=os.environ.get("APEXLOAD_URL", DEFAULT_API_URL).strip() or DEFAULT_API_URL,
            api_key=os.environ.get("APEXLOAD_API_KEY", "").strip(),
            bearer_token=os.environ.get("APEXLOAD_BEARER_TOKEN", "").strip(),
            default_project_id=os.environ.get("APEXLOAD_PROJECT_ID", "").strip(),
            timeout_seconds=timeout_seconds,
        )


def json_text(payload: JsonMap) -> str:
    return json.dumps(payload, separators=(",", ":"), sort_keys=True)


def guarded_response(message: str, *, code: str = "confirmation_required", remediation: str | None = None) -> JsonMap:
    return {
        "ok": False,
        "status_code": 0,
        "data": None,
        "error": {
            "code": code,
            "message": message,
            "remediation": remediation or "Set confirm=true after validating the intended side effect.",
        },
    }


def normalize_query(query: JsonMap | None) -> list[tuple[str, str]]:
    if not query:
        return []
    pairs: list[tuple[str, str]] = []
    for key, value in query.items():
        if value is None or value == "":
            continue
        if isinstance(value, (list, tuple, set)):
            for item in value:
                if item is not None and item != "":
                    pairs.append((str(key), str(item)))
        elif isinstance(value, bool):
            pairs.append((str(key), "true" if value else "false"))
        else:
            pairs.append((str(key), str(value)))
    return pairs


class APEXLoadClient:
    def __init__(
        self,
        config: APEXLoadClientConfig | None = None,
        *,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self.config = config or APEXLoadClientConfig.from_env()
        self._transport = transport

    @property
    def auth_headers(self) -> dict[str, str]:
        if self.config.api_key:
            return {"X-APEXLoad-API-Key": self.config.api_key}
        if self.config.bearer_token:
            return {"Authorization": f"Bearer {self.config.bearer_token}"}
        return {}

    def url_for(self, path: str, query: JsonMap | None = None) -> str:
        normalized_path = path if path.startswith("/") else f"/{path}"
        if normalized_path == "/metrics":
            base = self._controller_origin()
        else:
            base = self.config.base_url.rstrip("/")
        url = f"{base}{normalized_path}"
        encoded = urlencode(normalize_query(query), doseq=True)
        return f"{url}?{encoded}" if encoded else url

    async def request(
        self,
        method: str,
        path: str,
        *,
        query: JsonMap | None = None,
        payload: Any | None = None,
        headers: dict[str, str] | None = None,
    ) -> JsonMap:
        url = self.url_for(path, query)
        request_headers = {**self.auth_headers, **(headers or {})}
        if payload is not None:
            request_headers.setdefault("Content-Type", "application/json")
        try:
            async with httpx.AsyncClient(timeout=self.config.timeout_seconds, transport=self._transport) as client:
                response = await client.request(method.upper(), url, headers=request_headers, json=payload)
        except httpx.HTTPError as exc:
            return self._transport_error(path, exc)
        return self._response_payload(response, path)

    async def upload_file(
        self,
        path: str,
        file_path: str,
        *,
        fields: JsonMap | None = None,
        query: JsonMap | None = None,
    ) -> JsonMap:
        local_path = Path(file_path).expanduser()
        if not local_path.exists() or not local_path.is_file():
            return guarded_response(
                f"File is not accessible from the MCP server environment: {file_path}",
                code="file_not_found",
                remediation="Pass an absolute path to a file visible to the machine running the MCP server.",
            )

        url = self.url_for(path, query)
        form_fields = self._form_fields(fields)
        try:
            with local_path.open("rb") as file_handle:
                files = {"file": (local_path.name, file_handle, "application/octet-stream")}
                async with httpx.AsyncClient(timeout=self.config.timeout_seconds, transport=self._transport) as client:
                    response = await client.post(url, headers=self.auth_headers, data=form_fields, files=files)
        except httpx.HTTPError as exc:
            return self._transport_error(path, exc)
        return self._response_payload(response, path)

    def _controller_origin(self) -> str:
        parsed = urlparse(self.config.base_url.rstrip("/"))
        suffix = "/api/v1"
        path = parsed.path[:-len(suffix)] if parsed.path.endswith(suffix) else parsed.path
        return parsed._replace(path=path, params="", query="", fragment="").geturl().rstrip("/")

    def _response_payload(self, response: httpx.Response, path: str) -> JsonMap:
        ok = 200 <= response.status_code < 300
        parsed = self._parse_response_data(response)
        result = {
            "ok": ok,
            "status_code": response.status_code,
            "data": parsed if ok else None,
            "error": None if ok else self._api_error(response, parsed, path),
        }

        if ok and response.headers.get("X-APEXLOAD-Audit-Status") == "unavailable":
            result["warnings"] = [{"code": "activity_unavailable", "message": "Change saved; activity history could not be recorded."}]
        return result

    def _parse_response_data(self, response: httpx.Response) -> Any:
        content_type = response.headers.get("content-type", "").lower()
        if "application/json" in content_type:
            try:
                return response.json()
            except ValueError:
                return {"content_type": content_type, "text": response.text}
        text_like = content_type.startswith("text/") or any(token in content_type for token in ("csv", "html", "xml"))
        content_disposition = response.headers.get("content-disposition", "")
        return {
            "content_type": content_type or "application/octet-stream",
            "byte_length": len(response.content),
            "text_preview": response.text[:4000] if text_like else "",
            "content_disposition": content_disposition,
        }

    def _api_error(self, response: httpx.Response, parsed: Any, path: str) -> JsonMap:
        message = response.reason_phrase or "APEX Load API request failed"
        code = "api_error"
        if isinstance(parsed, dict):
            message = str(parsed.get("error") or parsed.get("message") or message)
            code = str(parsed.get("code") or code)
        return {
            "code": code,
            "message": message,
            "path": path,
            "remediation": self._remediation_for_status(response.status_code),
        }

    def _transport_error(self, path: str, exc: httpx.HTTPError) -> JsonMap:
        return {
            "ok": False,
            "status_code": 0,
            "data": None,
            "error": {
                "code": "transport_error",
                "message": str(exc),
                "path": path,
                "remediation": "Check APEXLOAD_URL, network reachability, and controller health.",
            },
        }

    def _remediation_for_status(self, status_code: int) -> str:
        if status_code in (401, 403):
            return "Check service-account credentials, token scopes, and APEX Load RBAC roles."
        if status_code == 404:
            return "Check the endpoint path and referenced APEX Load resource id."
        if status_code == 409:
            return "Resolve the reported scheduling, governance, or resource conflict and retry."
        if status_code == 400:
            return "Validate required fields, query parameters, and JSON payload shape."
        if status_code >= 500:
            return "Check controller logs and system health before retrying."
        return "Inspect the API error message and retry with corrected inputs."

    def _form_fields(self, fields: JsonMap | None) -> dict[str, str]:
        out: dict[str, str] = {}
        for key, value in (fields or {}).items():
            if value is None or value == "":
                continue
            if isinstance(value, bool):
                out[key] = "true" if value else "false"
            elif isinstance(value, (list, tuple, set)):
                out[key] = ",".join(str(item) for item in value)
            elif isinstance(value, (dict,)):
                out[key] = json.dumps(value, separators=(",", ":"))
            else:
                out[key] = str(value)
        return out


def apply_path_params(path: str, path_params: JsonMap | None) -> tuple[str | None, JsonMap | None]:
    params = path_params or {}
    missing: list[str] = []
    resolved = path
    for segment in path.split("{")[1:]:
        name = segment.split("}", 1)[0]
        value = params.get(name)
        if value is None or str(value) == "":
            missing.append(name)
            continue
        resolved = resolved.replace("{" + name + "}", quote(str(value), safe=""))
    if missing:
        return None, guarded_response(
            f"Missing required path parameter(s): {', '.join(missing)}",
            code="missing_path_params",
            remediation="Pass path_params with every placeholder required by this tool.",
        )
    return resolved, None
