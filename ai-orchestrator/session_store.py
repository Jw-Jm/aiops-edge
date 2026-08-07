"""Session persistence with LangGraph-compatible SQLite checkpointer"""
import json
import os
import sqlite3
import time


class SessionStore:
    """Custom session store - LangGraph SqliteSaver compatible"""

    def __init__(self, db_path="/tmp/ai-sessions.db"):
        self.db_path = db_path
        self._init_db()

    def _init_db(self):
        conn = sqlite3.connect(self.db_path)
        conn.execute("""
            CREATE TABLE IF NOT EXISTS sessions (
                session_id TEXT PRIMARY KEY,
                intent TEXT,
                service TEXT,
                messages TEXT,
                created_at REAL,
                updated_at REAL
            )
        """)
        conn.commit()
        conn.close()

    def save(self, session_id: str, intent: str, service: str, messages: list):
        conn = sqlite3.connect(self.db_path)
        conn.execute(
            "INSERT OR REPLACE INTO sessions VALUES (?, ?, ?, ?, ?, ?)",
            (session_id, intent, service, json.dumps(messages), time.time(), time.time())
        )
        conn.commit()
        conn.close()

    def load(self, session_id: str) -> dict | None:
        conn = sqlite3.connect(self.db_path)
        row = conn.execute("SELECT * FROM sessions WHERE session_id=?", (session_id,)).fetchone()
        conn.close()
        if row:
            return {
                "session_id": row[0], "intent": row[1], "service": row[2],
                "messages": json.loads(row[3]), "created_at": row[4], "updated_at": row[5]
            }
        return None

    def list_sessions(self, limit=50):
        conn = sqlite3.connect(self.db_path)
        rows = conn.execute("SELECT session_id, intent, service, updated_at FROM sessions ORDER BY updated_at DESC LIMIT ?", (limit,)).fetchall()
        conn.close()
        return [{"session_id": r[0], "intent": r[1], "service": r[2], "updated_at": r[3]} for r in rows]


session_store = SessionStore()
