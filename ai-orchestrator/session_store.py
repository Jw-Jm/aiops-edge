"""Session persistence with LangGraph-compatible SQLite checkpointer"""
from __future__ import annotations

import json
import os
import sqlite3
import time


class SessionStore:
    """Custom session store - LangGraph SqliteSaver compatible"""

    def __init__(self, db_path=None):
        import os as _os
        if db_path is None:
            data_dir = _os.environ.get("AIOPS_DATA_DIR", "/var/lib/aiops")
            db_path = _os.path.join(data_dir, "ai-sessions.db")
        self.db_path = db_path
        self._init_db()

    def _init_db(self):
        conn = sqlite3.connect(self.db_path)
        conn.execute("""
            CREATE TABLE IF NOT EXISTS sessions (
                session_id TEXT PRIMARY KEY,
                tenant_id TEXT NOT NULL DEFAULT '',
                cluster_id TEXT NOT NULL DEFAULT '',
                intent TEXT,
                service TEXT,
                messages TEXT,
                created_at REAL,
                updated_at REAL
            )
        """)
        columns = {row[1] for row in conn.execute("PRAGMA table_info(sessions)").fetchall()}
        if "tenant_id" not in columns:
            conn.execute("ALTER TABLE sessions ADD COLUMN tenant_id TEXT NOT NULL DEFAULT ''")
        if "cluster_id" not in columns:
            conn.execute("ALTER TABLE sessions ADD COLUMN cluster_id TEXT NOT NULL DEFAULT ''")
        conn.commit()
        conn.close()

    def save(self, session_id: str, intent: str, service: str, messages: list,
             tenant_id: str = "", cluster_id: str = ""):
        conn = sqlite3.connect(self.db_path)
        conn.execute(
            "INSERT OR REPLACE INTO sessions "
            "(session_id, tenant_id, cluster_id, intent, service, messages, created_at, updated_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
            (session_id, tenant_id, cluster_id, intent, service, json.dumps(messages), time.time(), time.time())
        )
        conn.commit()
        conn.close()

    def load(self, session_id: str) -> dict | None:
        conn = sqlite3.connect(self.db_path)
        row = conn.execute(
            "SELECT session_id, tenant_id, cluster_id, intent, service, messages, created_at, updated_at "
            "FROM sessions WHERE session_id=?", (session_id,)).fetchone()
        conn.close()
        if row:
            return {
                "session_id": row[0], "tenant_id": row[1], "cluster_id": row[2],
                "intent": row[3], "service": row[4], "messages": json.loads(row[5]),
                "created_at": row[6], "updated_at": row[7]
            }
        return None

    def list_sessions(self, limit=50):
        conn = sqlite3.connect(self.db_path)
        rows = conn.execute("SELECT session_id, intent, service, updated_at FROM sessions ORDER BY updated_at DESC LIMIT ?", (limit,)).fetchall()
        conn.close()
        return [{"session_id": r[0], "intent": r[1], "service": r[2], "updated_at": r[3]} for r in rows]

    def count_before(self, cutoff_epoch: float, tenant_id: str, cluster_id: str = "") -> int:
        conn = sqlite3.connect(self.db_path)
        where = ["updated_at < ?", "tenant_id = ?"]
        args = [cutoff_epoch, tenant_id]
        if cluster_id:
            where.append("cluster_id = ?")
            args.append(cluster_id)
        row = conn.execute("SELECT COUNT(*) FROM sessions WHERE " + " AND ".join(where), args).fetchone()
        conn.close()
        return int(row[0] if row else 0)

    def delete_before(self, cutoff_epoch: float, tenant_id: str, cluster_id: str = "") -> dict:
        conn = sqlite3.connect(self.db_path)
        try:
            where = ["updated_at < ?", "tenant_id = ?"]
            args = [cutoff_epoch, tenant_id]
            if cluster_id:
                where.append("cluster_id = ?")
                args.append(cluster_id)
            predicate = " AND ".join(where)
            session_ids = [row[0] for row in conn.execute(
                "SELECT session_id FROM sessions WHERE " + predicate, args).fetchall()]
            if not session_ids:
                conn.commit()
                return {"deleted_sessions": 0, "deleted_checkpoints": 0, "deleted_writes": 0}

            placeholders = ",".join("?" for _ in session_ids)
            params = list(session_ids)
            deleted_checkpoints = 0
            deleted_writes = 0
            try:
                cursor = conn.execute("DELETE FROM checkpoints WHERE thread_id IN (" + placeholders + ")", params)
                deleted_checkpoints = cursor.rowcount
            except sqlite3.OperationalError:
                pass
            try:
                cursor = conn.execute("DELETE FROM writes WHERE thread_id IN (" + placeholders + ")", params)
                deleted_writes = cursor.rowcount
            except sqlite3.OperationalError:
                pass
            cursor = conn.execute("DELETE FROM sessions WHERE session_id IN (" + placeholders + ")", params)
            conn.commit()
            return {
                "deleted_sessions": cursor.rowcount,
                "deleted_checkpoints": deleted_checkpoints,
                "deleted_writes": deleted_writes,
            }
        finally:
            conn.close()


session_store = SessionStore()
