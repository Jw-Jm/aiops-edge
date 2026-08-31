"""Uvicorn entrypoint with application-level client certificate identity checks.

Uvicorn's stock ASGI scope does not expose the peer certificate.  The custom
HTTP protocol below obtains it from the TLS transport and delegates the
authorization decision to :func:`mtls.guard_app_with_client_san`.  This keeps
the identity source in the TLS connection and prevents request headers from
being used as an identity substitute.
"""
from __future__ import annotations

import argparse
import os
import ssl
from typing import Any

from uvicorn.config import Config
from uvicorn.protocols.http.h11_impl import H11Protocol
from uvicorn.server import Server

from mtls import guard_app_with_client_san


class ClientSANH11Protocol(H11Protocol):
    """H11 protocol that rejects requests with an unapproved client SAN."""

    def __init__(self, config: Config, server_state, app_state: dict[str, Any], _loop=None) -> None:
        super().__init__(config, server_state, app_state, _loop)
        allowed_sans = os.getenv("AIOPS_TLS_CLIENT_SAN", "").strip()
        required = os.getenv("AIOPS_MTLS_REQUIRED", "").strip().lower() == "true"
        if required and not allowed_sans:
            raise RuntimeError("AIOPS_MTLS_REQUIRED=true requires AIOPS_TLS_CLIENT_SAN")
        self.app = guard_app_with_client_san(self.app, self._peer_certificate, allowed_sans)

    def _peer_certificate(self) -> dict | None:
        ssl_object = self.transport.get_extra_info("ssl_object")
        if ssl_object is None:
            return None
        return ssl_object.getpeercert()


def run(
    app: str,
    *,
    host: str,
    port: int,
    timeout_keep_alive: int,
    limit_concurrency: int,
    ssl_keyfile: str | None,
    ssl_certfile: str | None,
    ssl_ca_certs: str | None,
    ssl_cert_reqs: int = ssl.CERT_REQUIRED,
) -> None:
    """Run an ASGI app with the SAN-validating protocol.

    The adapter is intentionally TLS-only.  Callers that do not provide the
    three TLS files should use the stock Uvicorn entrypoint instead.
    """
    if not ssl_keyfile or not ssl_certfile or not ssl_ca_certs:
        raise SystemExit("mTLS server requires --ssl-keyfile, --ssl-certfile and --ssl-ca-certs")
    if ssl_cert_reqs != ssl.CERT_REQUIRED:
        raise SystemExit("mTLS server requires --ssl-cert-reqs 2 (CERT_REQUIRED)")
    config = Config(
        app,
        host=host,
        port=port,
        http=ClientSANH11Protocol,
        timeout_keep_alive=timeout_keep_alive,
        limit_concurrency=limit_concurrency,
        ssl_keyfile=ssl_keyfile,
        ssl_certfile=ssl_certfile,
        ssl_ca_certs=ssl_ca_certs,
        ssl_cert_reqs=ssl.CERT_REQUIRED,
    )
    Server(config).run()


def _parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("app", help="ASGI import string, for example main:app")
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8080)
    parser.add_argument("--timeout-keep-alive", type=int, default=120)
    parser.add_argument("--limit-concurrency", type=int, default=20)
    parser.add_argument("--ssl-keyfile", required=True)
    parser.add_argument("--ssl-certfile", required=True)
    parser.add_argument("--ssl-ca-certs", required=True)
    parser.add_argument("--ssl-cert-reqs", type=int, default=ssl.CERT_REQUIRED)
    return parser.parse_args()


if __name__ == "__main__":
    args = _parse_args()
    run(
        args.app,
        host=args.host,
        port=args.port,
        timeout_keep_alive=args.timeout_keep_alive,
        limit_concurrency=args.limit_concurrency,
        ssl_keyfile=args.ssl_keyfile,
        ssl_certfile=args.ssl_certfile,
        ssl_ca_certs=args.ssl_ca_certs,
        ssl_cert_reqs=args.ssl_cert_reqs,
    )
