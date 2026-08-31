from __future__ import annotations

import asyncio
import datetime as dt
import importlib.util
import ssl
import tempfile
from pathlib import Path

import mtls
import pytest
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID
from uvicorn.protocols.http.h11_impl import H11Protocol
from uvicorn.config import Config
from uvicorn.server import Server

from mtls_server import ClientSANH11Protocol


def test_client_certificate_san_allowlist_matches_dns_and_uri_exactly():
    certificate = {
        "subjectAltName": (
            ("DNS", "query-api.observability.svc.cluster.local"),
            ("URI", "spiffe://observability/query-api"),
        )
    }
    matcher = getattr(mtls, "client_certificate_san_allowed", None)
    assert matcher is not None
    assert matcher(certificate, "query-api.observability.svc.cluster.local") is True
    assert matcher(certificate, "spiffe://observability/query-api") is True
    assert matcher(certificate, "other.observability.svc.cluster.local") is False


def test_client_certificate_san_allowlist_fails_closed_for_missing_certificate():
    matcher = getattr(mtls, "client_certificate_san_allowed", None)
    assert matcher is not None
    assert matcher(None, "query-api.observability.svc.cluster.local") is False
    assert matcher({}, "query-api.observability.svc.cluster.local") is False
    assert matcher({"subjectAltName": ()}, "") is False


@pytest.mark.asyncio
async def test_san_guard_rejects_wrong_peer_before_entering_asgi_app():
    guard_factory = getattr(mtls, "guard_app_with_client_san", None)
    assert guard_factory is not None
    entered = False

    async def app(scope, receive, send):
        nonlocal entered
        entered = True

    guarded = guard_factory(
        app,
        lambda: {"subjectAltName": (("DNS", "wrong.service"),)},
        "query-api.service",
    )
    sent = []

    async def send(message):
        sent.append(message)

    await guarded({"type": "http"}, lambda: None, send)
    assert entered is False
    assert sent[0]["status"] == 403


def test_uvicorn_san_protocol_adapter_is_available():
    assert importlib.util.find_spec("mtls_server") is not None


@pytest.mark.asyncio
async def test_uvicorn_protocol_adapter_uses_peer_certificate_not_headers(monkeypatch):
    entered = False

    async def app(scope, receive, send):
        nonlocal entered
        entered = True

    class SSLObject:
        def getpeercert(self):
            return {"subjectAltName": (("DNS", "wrong.service"),)}

    class Transport:
        def get_extra_info(self, name):
            assert name == "ssl_object"
            return SSLObject()

    def fake_init(self, config, server_state, app_state, _loop=None):
        self.app = app
        self.transport = Transport()

    monkeypatch.setattr(H11Protocol, "__init__", fake_init)
    monkeypatch.setenv("AIOPS_MTLS_REQUIRED", "true")
    monkeypatch.setenv("AIOPS_TLS_CLIENT_SAN", "query-api.service")
    protocol = ClientSANH11Protocol(None, None, {})
    sent = []

    async def send(message):
        sent.append(message)

    await protocol.app({"type": "http", "headers": [(b"x-client-san", b"query-api.service")]}, None, send)
    assert entered is False
    assert sent[0]["status"] == 403


def _test_certificate_authority(directory: Path):
    private = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "AIOps test CA")])
    certificate = (
        x509.CertificateBuilder()
        .subject_name(name)
        .issuer_name(name)
        .public_key(private.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(dt.datetime.now(dt.UTC) - dt.timedelta(minutes=1))
        .not_valid_after(dt.datetime.now(dt.UTC) + dt.timedelta(hours=1))
        .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
        .sign(private, hashes.SHA256())
    )
    path = directory / "ca.crt"
    path.write_bytes(certificate.public_bytes(serialization.Encoding.PEM))
    return private, name, path


def _test_certificate(directory, stem, ca_private, ca_name, san):
    private = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, stem)])
    certificate = (
        x509.CertificateBuilder()
        .subject_name(name)
        .issuer_name(ca_name)
        .public_key(private.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(dt.datetime.now(dt.UTC) - dt.timedelta(minutes=1))
        .not_valid_after(dt.datetime.now(dt.UTC) + dt.timedelta(hours=1))
        .add_extension(x509.SubjectAlternativeName([x509.DNSName(san)]), critical=False)
        .sign(ca_private, hashes.SHA256())
    )
    cert_path = directory / f"{stem}.crt"
    key_path = directory / f"{stem}.key"
    cert_path.write_bytes(certificate.public_bytes(serialization.Encoding.PEM))
    key_path.write_bytes(
        private.private_bytes(
            serialization.Encoding.PEM,
            serialization.PrivateFormat.TraditionalOpenSSL,
            serialization.NoEncryption(),
        )
    )
    return cert_path, key_path


@pytest.mark.asyncio
async def test_uvicorn_protocol_rejects_wrong_san_over_real_tls(monkeypatch):
    with tempfile.TemporaryDirectory(prefix="aiops-mtls-test-") as raw:
        directory = Path(raw)
        ca_private, ca_name, ca_path = _test_certificate_authority(directory)
        server_cert, server_key = _test_certificate(
            directory, "server", ca_private, ca_name, "localhost"
        )
        good_cert, good_key = _test_certificate(
            directory, "good", ca_private, ca_name, "allowed.service"
        )
        bad_cert, bad_key = _test_certificate(
            directory, "bad", ca_private, ca_name, "wrong.service"
        )

        async def app(scope, receive, send):
            await send({"type": "http.response.start", "status": 200, "headers": []})
            await send({"type": "http.response.body", "body": b"ok"})

        monkeypatch.setenv("AIOPS_MTLS_REQUIRED", "true")
        monkeypatch.setenv("AIOPS_TLS_CLIENT_SAN", "allowed.service")
        server = Server(
            Config(
                app,
                host="127.0.0.1",
                port=0,
                http=ClientSANH11Protocol,
                ssl_keyfile=str(server_key),
                ssl_certfile=str(server_cert),
                ssl_ca_certs=str(ca_path),
                ssl_cert_reqs=ssl.CERT_REQUIRED,
            )
        )
        task = asyncio.create_task(server.serve())
        try:
            for _ in range(100):
                if server.started:
                    break
                await asyncio.sleep(0.02)
            assert server.started and server.servers
            port = server.servers[0].sockets[0].getsockname()[1]

            async def request(cert, key):
                # The server still validates the client chain against ca_path;
                # this client context deliberately skips server-name checking
                # because the ephemeral test server certificate is not the
                # identity under test.
                context = ssl._create_unverified_context()
                context.load_cert_chain(str(cert), str(key))
                reader, writer = await asyncio.open_connection(
                    "127.0.0.1", port, ssl=context, server_hostname="localhost"
                )
                writer.write(
                    b"GET /health HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
                )
                await writer.drain()
                response = await reader.read()
                writer.close()
                await writer.wait_closed()
                return response.split(b" ", 2)[1]

            assert await request(bad_cert, bad_key) == b"403"
            assert await request(good_cert, good_key) == b"200"
        finally:
            server.should_exit = True
            await task
