# flow_engine/store.py
from __future__ import annotations

import json
import os
import sqlite3
import uuid
import time


def _now():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ")


class FlowStore:
    def __init__(self, db_path: str = None):
        self.db_path = db_path or os.environ.get("FLOWS_DB", "/data/aiops-flows.db")
        os.makedirs(os.path.dirname(self.db_path), exist_ok=True) if os.path.dirname(self.db_path) else None
        self._conn = sqlite3.connect(self.db_path, check_same_thread=False)
        self._conn.row_factory = sqlite3.Row
        self._init_schema()

    def _init_schema(self):
        self._conn.executescript("""
        CREATE TABLE IF NOT EXISTS flows (
            id TEXT PRIMARY KEY, name TEXT, description TEXT,
            enabled INTEGER DEFAULT 1, version INTEGER DEFAULT 1,
            graph_json TEXT, created_at TEXT, updated_at TEXT
        );
        CREATE TABLE IF NOT EXISTS flow_runs (
            run_id TEXT PRIMARY KEY, flow_id TEXT, flow_version INTEGER,
            status TEXT, trigger_type TEXT, trigger_json TEXT,
            context_json TEXT, error TEXT, created_at TEXT
        );
        CREATE TABLE IF NOT EXISTS flow_run_nodes (
            run_id TEXT, node_id TEXT, node_type TEXT, node_name TEXT,
            status TEXT, input_json TEXT, output_json TEXT,
            fired_port TEXT, error TEXT
        );
        """)
        self._conn.commit()

    def save_flow(self, flow: dict) -> str:
        fid = flow.get("id") or f"flow_{uuid.uuid4().hex[:8]}"
        now = _now()
        existing = self.get_flow(fid)
        version = (existing["version"] + 1) if existing else 1
        self._conn.execute(
            "INSERT OR REPLACE INTO flows (id,name,description,enabled,version,graph_json,created_at,updated_at) "
            "VALUES (?,?,?,?,?,?,?,?)",
            (fid, flow.get("name", ""), flow.get("description", ""),
             int(flow.get("enabled", True)), version,
             json.dumps(flow.get("graph", flow.get("graph_json")), ensure_ascii=False),
             existing["created_at"] if existing else now, now))
        self._conn.commit()
        return fid

    def get_flow(self, flow_id: str) -> dict | None:
        r = self._conn.execute("SELECT * FROM flows WHERE id=?", (flow_id,)).fetchone()
        if not r:
            return None
        d = dict(r)
        d["enabled"] = bool(d["enabled"])
        try:
            d["graph"] = json.loads(d["graph_json"])
        except Exception:
            d["graph"] = {}
        return d

    def list_flows(self) -> list[dict]:
        return [self.get_flow(r["id"]) for r in self._conn.execute("SELECT id FROM flows ORDER BY updated_at DESC")]

    def delete_flow(self, flow_id: str) -> bool:
        cur = self._conn.execute("DELETE FROM flows WHERE id=?", (flow_id,))
        self._conn.commit()
        return cur.rowcount > 0

    def toggle_flow(self, flow_id: str) -> bool:
        f = self.get_flow(flow_id)
        if not f:
            return False
        self._conn.execute("UPDATE flows SET enabled=?, updated_at=? WHERE id=?",
                           (int(not f["enabled"]), _now(), flow_id))
        self._conn.commit()
        return True

    def create_run(self, flow_id, flow_version, trigger_type, trigger_json) -> str:
        rid = str(uuid.uuid4())
        self._conn.execute(
            "INSERT INTO flow_runs (run_id,flow_id,flow_version,status,trigger_type,trigger_json,created_at) "
            "VALUES (?,?,?,?,?,?,?)",
            (rid, flow_id, flow_version, "pending", trigger_type, trigger_json, _now()))
        self._conn.commit()
        return rid

    def update_run_status(self, run_id, status, error="", context_json=""):
        self._conn.execute("UPDATE flow_runs SET status=?, error=?, context_json=? WHERE run_id=?",
                           (status, error, context_json, run_id))
        self._conn.commit()

    def get_run(self, run_id) -> dict | None:
        r = self._conn.execute("SELECT * FROM flow_runs WHERE run_id=?", (run_id,)).fetchone()
        return dict(r) if r else None

    def list_runs(self, flow_id) -> list[dict]:
        return [dict(r) for r in self._conn.execute(
            "SELECT * FROM flow_runs WHERE flow_id=? ORDER BY created_at DESC", (flow_id,))]

    def save_run_node(self, run_id, node_id, node_type, status,
                      input_json, output_json, fired_port, error=""):
        self._conn.execute(
            "INSERT INTO flow_run_nodes (run_id,node_id,node_type,node_name,status,input_json,output_json,fired_port,error) "
            "VALUES (?,?,?,?,?,?,?,?,?)",
            (run_id, node_id, node_type, node_id, status, input_json, output_json, fired_port, error))
        self._conn.commit()

    def get_run_nodes(self, run_id) -> list[dict]:
        return [dict(r) for r in self._conn.execute(
            "SELECT * FROM flow_run_nodes WHERE run_id=? ORDER BY rowid", (run_id,))]
