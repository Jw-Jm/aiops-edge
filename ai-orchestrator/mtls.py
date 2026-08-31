"""mTLS helpers for orchestrator → internal service calls."""
from __future__ import annotations

import os
import logging
import ssl
import urllib.request
from collections.abc import Awaitable, Callable

_LOGGER = logging.getLogger(__name__)


def client_certificate_san_allowed(certificate: dict | None, allowed_sans: str) -> bool:
    """Return whether a peer certificate contains an exact allowed SAN.

    ``SSLSocket.getpeercert()`` exposes SANs as ``(kind, value)`` tuples.  Only
    DNS and URI identities are accepted; missing certificate material or an
    empty allowlist fails closed so callers cannot accidentally broaden the
    service trust boundary.
    """
    if not certificate or not allowed_sans.strip():
        return False
    allowed = {
        value.strip()
        for value in allowed_sans.split(",")
        if value.strip()
    }
    if not allowed:
        return False
    for kind, value in certificate.get("subjectAltName", ()):
        if kind in {"DNS", "URI"} and value in allowed:
            return True
    return False


def guard_app_with_client_san(
    app: Callable[..., Awaitable[None]],
    peer_certificate: Callable[[], dict | None],
    allowed_sans: str,
) -> Callable[..., Awaitable[None]]:
    """Wrap an ASGI app with a fail-closed client SAN check.

    The peer certificate is obtained from the server protocol's TLS
    transport, never from request headers.  Lifespan events are passed
    through; HTTP requests are rejected before the wrapped application runs.
    """

    async def guarded(scope, receive, send):
        if scope.get("type") == "http":
            certificate = peer_certificate()
            if client_certificate_san_allowed(certificate, allowed_sans):
                await app(scope, receive, send)
                return
            peer_sans = tuple(
                f"{kind}:{value}"
                for kind, value in (certificate or {}).get("subjectAltName", ())
                if kind in {"DNS", "URI"}
            )
            _LOGGER.warning(
                "mTLS client SAN rejected peer_certificate_present=%s peer_sans=%s",
                bool(certificate),
                peer_sans,
            )
            await send(
                {
                    "type": "http.response.start",
                    "status": 403,
                    "headers": [(b"content-type", b"text/plain; charset=utf-8")],
                }
            )
            await send(
                {
                    "type": "http.response.body",
                    "body": b"client certificate SAN is not allowed",
                }
            )
            return
        await app(scope, receive, send)

    return guarded


def client_context() -> ssl.SSLContext | None:
    cert = os.getenv("AIOPS_TLS_CERT_FILE", "").strip()
    key = os.getenv("AIOPS_TLS_KEY_FILE", "").strip()
    ca = os.getenv("AIOPS_TLS_CLIENT_CA_FILE", "").strip()
    required = os.getenv("AIOPS_MTLS_REQUIRED", "").strip().lower() == "true"
    if not cert or not key or not ca:
        if required:
            raise RuntimeError("mTLS is required but client certificate/key/CA are not configured")
        return None
    context = ssl.create_default_context(cafile=ca)
    context.minimum_version = ssl.TLSVersion.TLSv1_2
    context.load_cert_chain(certfile=cert, keyfile=key)
    return context


def urlopen(request, *, timeout: float):
    context = client_context()
    if context is None:
        return urllib.request.urlopen(request, timeout=timeout)
    return urllib.request.urlopen(request, timeout=timeout, context=context)
