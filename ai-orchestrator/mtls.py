"""mTLS helpers for orchestrator → internal service calls."""
from __future__ import annotations

import os
import ssl
import urllib.request


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
