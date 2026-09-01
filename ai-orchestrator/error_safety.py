"""Stable, non-sensitive error boundaries for durable runtime records.

Exceptions can contain provider URLs, SQL, credentials, or internal topology.
They are useful to server-side diagnostics but must not cross the durable Run
event/result boundary.  This module deliberately keeps the public contract
small: callers receive a stable error code and a generic message, while normal
business payloads remain intact.
"""

from __future__ import annotations

import re
from collections.abc import Mapping
from typing import Any


_SAFE_ERROR_CODE = re.compile(r"^[A-Z][A-Z0-9_]{1,63}$")
_SENSITIVE_KEYS = {
    "error",
    "error_message",
    "exception",
    "traceback",
    "stack",
    "stacktrace",
    "token",
    "access_token",
    "refresh_token",
    "api_key",
    "apikey",
    "password",
    "secret",
    "client_secret",
    "authorization",
}

_ERROR_MESSAGES = {
    "BRAIN_ERROR": "investigation failed",
    "BRAIN_EXCEPTION": "investigation failed",
    "RCA_V2_UNAVAILABLE": "RCA engine unavailable",
    "TOOL_FAILED": "investigation tool failed",
    "GRAPH_CONTEXT_FINALIZE_FAILED": "RCA context finalization failed",
    "GRAPH_UNAVAILABLE": "graph data unavailable",
    "INSUFFICIENT_EVIDENCE": "insufficient evidence",
}


def stable_error_code(value: Any, default: str = "INTERNAL_ERROR") -> str:
    """Return an allow-listed shape suitable for a wire/audit error code."""

    candidate = str(value or "").strip().upper()
    if _SAFE_ERROR_CODE.fullmatch(candidate):
        return candidate
    fallback = str(default or "INTERNAL_ERROR").strip().upper()
    return fallback if _SAFE_ERROR_CODE.fullmatch(fallback) else "INTERNAL_ERROR"


def public_error_message(code: Any, default: str = "investigation failed") -> str:
    """Map a stable code to a generic message; never echo exception text."""

    normalized = stable_error_code(code)
    return _ERROR_MESSAGES.get(normalized, default)


def sanitize_runtime_payload(value: Any) -> Any:
    """Remove credential/exception fields recursively from a runtime payload.

    This is intentionally structural rather than pattern based so valid log or
    RCA content is not corrupted.  Error-bearing fields are represented by the
    caller as ``error_code`` before this function is used.
    """

    if isinstance(value, Mapping):
        output: dict[str, Any] = {}
        for key, item in value.items():
            key_text = str(key)
            if key_text.lower() in _SENSITIVE_KEYS:
                continue
            output[key_text] = sanitize_runtime_payload(item)
        return output
    if isinstance(value, list):
        return [sanitize_runtime_payload(item) for item in value]
    if isinstance(value, tuple):
        return [sanitize_runtime_payload(item) for item in value]
    return value
