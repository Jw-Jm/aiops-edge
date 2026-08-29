import os
import sqlite3
import sys
import tempfile
from types import SimpleNamespace

from fastapi import FastAPI
from fastapi.testclient import TestClient

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
os.environ["AIOPS_DATA_DIR"] = tempfile.mkdtemp(prefix="aiops-cleanup-test-")

from data_cleanup_api import configure_data_cleanup_runtime, router
from session_store import SessionStore


def _client(tmp_path, monkeypatch):
    db_path = tmp_path / "sessions.db"
    sessions = SessionStore(str(db_path))
    sessions.save("old", "diagnosis", "svc", [], tenant_id="tenant-1", cluster_id="cluster-1")
    sessions.save("new", "diagnosis", "svc", [], tenant_id="tenant-1", cluster_id="cluster-1")
    sessions.save("other-tenant", "diagnosis", "svc", [], tenant_id="tenant-2", cluster_id="cluster-1")

    conn = sqlite3.connect(str(db_path))
    conn.execute("UPDATE sessions SET updated_at=? WHERE session_id='old'", (1782864000.0,))
    conn.execute("UPDATE sessions SET updated_at=? WHERE session_id='new'", (1785542400.0,))
    conn.execute("UPDATE sessions SET updated_at=? WHERE session_id='other-tenant'", (1782864000.0,))
    conn.execute("CREATE TABLE checkpoints (thread_id TEXT, checkpoint_id INTEGER)")
    conn.execute("CREATE TABLE writes (thread_id TEXT, task_id TEXT)")
    conn.executemany("INSERT INTO checkpoints VALUES (?, ?)", [("old", 1), ("new", 2), ("other-tenant", 3)])
    conn.executemany("INSERT INTO writes VALUES (?, ?)", [("old", "w1"), ("new", "w2"), ("other-tenant", "w3")])
    conn.commit()
    brain = SimpleNamespace(_db_path=str(db_path), _conn=conn)

    monkeypatch.setenv("QUERY_TO_ORCHESTRATOR_TOKEN", "query-to-orchestrator")
    configure_data_cleanup_runtime(lambda: brain, lambda: sessions)
    app = FastAPI()
    app.include_router(router)
    return TestClient(app), conn


def test_ai_session_cleanup_requires_internal_token(tmp_path, monkeypatch):
    client, conn = _client(tmp_path, monkeypatch)
    try:
        response = client.post(
            "/internal/v1/data-cleanups/ai-sessions",
            json={"preview": True, "cutoff_at": "2026-08-01T00:00:00Z", "tenant_id": "tenant-1"},
        )
        assert response.status_code == 401
    finally:
        conn.close()


def test_ai_session_cleanup_previews_and_deletes_only_matching_old_sessions(tmp_path, monkeypatch):
    client, conn = _client(tmp_path, monkeypatch)
    headers = {"X-Internal-Token": "query-to-orchestrator"}
    body = {
        "preview": True,
        "cutoff_at": "2026-08-01T00:00:00Z",
        "tenant_id": "tenant-1",
        "cluster_id": "cluster-1",
        "operation_id": "op-1",
        "request_digest": "digest-1",
    }
    headers["X-Cleanup-Operation-Id"] = "op-1"
    headers["X-Cleanup-Request-Digest"] = "digest-1"
    try:
        preview = client.post("/internal/v1/data-cleanups/ai-sessions", json=body, headers=headers)
        assert preview.status_code == 200
        assert preview.json() == {"scope": "ai_sessions", "table": "sessions", "estimated_rows": 1}

        body["preview"] = False
        deleted = client.post("/internal/v1/data-cleanups/ai-sessions", json=body, headers=headers)
        assert deleted.status_code == 200
        assert deleted.json()["deleted_sessions"] == 1
        assert deleted.json()["deleted_checkpoints"] == 1
        assert deleted.json()["deleted_writes"] == 1

        rows = conn.execute("SELECT session_id FROM sessions ORDER BY session_id").fetchall()
        assert rows == [("new",), ("other-tenant",)]
        assert conn.execute("SELECT thread_id FROM checkpoints ORDER BY thread_id").fetchall() == [("new",), ("other-tenant",)]
        assert conn.execute("SELECT thread_id FROM writes ORDER BY thread_id").fetchall() == [("new",), ("other-tenant",)]
    finally:
        conn.close()
