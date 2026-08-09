"""Agent/Report/Knowledge/Rule 四个 Store。MySQL 不可用降级为内存。"""
import json
import db


class AgentStore:
    """AI Agent 持久化。"""

    def __init__(self):
        self._mem: dict[str, dict] = {}

    def upsert(self, name: str, role: str, goal: str, backstory: str, enabled: bool, builtin: bool):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "INSERT INTO agents (name, role, goal, backstory, enabled, builtin) "
                        "VALUES (%s,%s,%s,%s,%s,%s) "
                        "ON DUPLICATE KEY UPDATE role=%s, goal=%s, backstory=%s, enabled=%s, builtin=%s",
                        (name, role, goal, backstory, enabled, builtin,
                         role, goal, backstory, enabled, builtin),
                    )
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem[name] = {"name": name, "role": role, "goal": goal,
                           "backstory": backstory, "enabled": enabled, "builtin": builtin}

    def list(self):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("SELECT * FROM agents ORDER BY name")
                    rows = cur.fetchall()
                if rows:
                    return [dict(r) for r in rows]
            except Exception:
                pass
            finally:
                conn.close()
        return list(self._mem.values())

    def delete(self, name: str):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("DELETE FROM agents WHERE name=%s", (name,))
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem.pop(name, None)

    def toggle(self, name: str):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("UPDATE agents SET enabled = NOT enabled WHERE name=%s", (name,))
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        if name in self._mem:
            self._mem[name]["enabled"] = not self._mem[name]["enabled"]


class ReportStore:
    """报告元数据持久化。文件仍存 MinIO。"""

    def __init__(self):
        self._mem: list[dict] = []

    def save(self, data: dict):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "INSERT INTO reports (task_id, service_name, report_type, verdict, "
                        "risk_score, summary, content, file_key) VALUES (%s,%s,%s,%s,%s,%s,%s,%s)",
                        (data.get("task_id", ""), data.get("service_name", ""),
                         data.get("report_type", ""), data.get("verdict", ""),
                         float(data.get("risk_score", 0) or 0), data.get("summary", ""),
                         data.get("content", ""), data.get("file_key")),
                    )
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem.append(dict(data))

    def list(self, service=None, page=1, size=50):
        offset = (page - 1) * size
        if db.db_available():
            conn = db.get_conn()
            try:
                w = " WHERE service_name=%s" if service else ""
                vals = (service,) if service else ()
                with conn.cursor() as cur:
                    cur.execute("SELECT COUNT(*) AS total FROM reports" + w, vals)
                    total = cur.fetchone()["total"]
                    cur.execute("SELECT * FROM reports" + w + " ORDER BY created_at DESC LIMIT %s OFFSET %s",
                                vals + (size, offset))
                    rows = cur.fetchall()
                if rows is not None:
                    return {"items": [dict(r) for r in rows], "total": total}
            except Exception:
                pass
            finally:
                conn.close()
        mem = [r for r in self._mem if not service or r.get("service_name") == service]
        return {"items": mem[offset:offset + size], "total": len(mem)}


class KnowledgeStore:
    """知识库条目持久化（含代码索引 code_ref）。"""

    def __init__(self):
        self._mem: dict[int, dict] = {}
        self._seq = 0

    def add(self, title: str, content: str, source: str = "manual", tags: str = "", code_ref: dict = None):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "INSERT INTO knowledge_base (title, content, source, tags, code_ref) "
                        "VALUES (%s,%s,%s,%s,%s)",
                        (title, content, source, tags,
                         json.dumps(code_ref, ensure_ascii=False) if code_ref else None),
                    )
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._seq += 1
        self._mem[self._seq] = {"id": self._seq, "title": title, "content": content,
                                "source": source, "tags": tags, "code_ref": code_ref}
        return self._seq

    def search(self, q: str):
        ql = q.lower()
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "SELECT * FROM knowledge_base WHERE title LIKE %s OR content LIKE %s OR tags LIKE %s "
                        "ORDER BY id DESC LIMIT 50", (f"%{q}%", f"%{q}%", f"%{q}%"))
                    rows = cur.fetchall()
                if rows:
                    return {"items": [dict(r) for r in rows], "total": len(rows)}
            except Exception:
                pass
            finally:
                conn.close()
        mem = [k for k in self._mem.values()
               if ql in k["title"].lower() or ql in k["content"].lower() or ql in k["tags"].lower()]
        return {"items": mem, "total": len(mem)}

    def list(self, page=1, size=50):
        offset = (page - 1) * size
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("SELECT COUNT(*) AS total FROM knowledge_base")
                    total = cur.fetchone()["total"]
                    cur.execute("SELECT * FROM knowledge_base ORDER BY id DESC LIMIT %s OFFSET %s", (size, offset))
                    rows = cur.fetchall()
                if rows is not None:
                    return {"items": [dict(r) for r in rows], "total": total}
            except Exception:
                pass
            finally:
                conn.close()
        items = list(self._mem.values())
        return {"items": items[offset:offset + size], "total": len(items)}

    def delete(self, kid: int):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("DELETE FROM knowledge_base WHERE id=%s", (kid,))
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem.pop(kid, None)


class RuleStore:
    """规则持久化（自定义 Alert Rules 字段模型）。"""

    def __init__(self):
        self._mem: dict[str, dict] = {}

    def save(self, rule_key: str, name: str, kind: str, severity: str, enabled: bool,
             scope_type: str, join_mode: str, conditions_json: dict, source_type: str):
        conds = json.dumps(conditions_json, ensure_ascii=False) if conditions_json else None
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute(
                        "INSERT INTO rules (rule_key, name, kind, severity, enabled, scope_type, "
                        "join_mode, conditions_json, source_type) "
                        "VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s) "
                        "ON DUPLICATE KEY UPDATE name=%s, kind=%s, severity=%s, enabled=%s, "
                        "scope_type=%s, join_mode=%s, conditions_json=%s, source_type=%s",
                        (rule_key, name, kind, severity, enabled, scope_type, join_mode, conds, source_type,
                         name, kind, severity, enabled, scope_type, join_mode, conds, source_type),
                    )
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem[rule_key] = {"rule_key": rule_key, "name": name, "kind": kind,
                               "severity": severity, "enabled": enabled, "scope_type": scope_type,
                               "join_mode": join_mode, "conditions_json": conditions_json,
                               "source_type": source_type}

    def list(self):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("SELECT * FROM rules WHERE deleted_at IS NULL ORDER BY name")
                    rows = cur.fetchall()
                if rows:
                    return [dict(r) for r in rows]
            except Exception:
                pass
            finally:
                conn.close()
        return list(self._mem.values())

    def delete(self, rule_key: str):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("UPDATE rules SET deleted_at = NOW() WHERE rule_key=%s", (rule_key,))
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        self._mem.pop(rule_key, None)

    def toggle(self, rule_key: str):
        if db.db_available():
            conn = db.get_conn()
            try:
                with conn.cursor() as cur:
                    cur.execute("UPDATE rules SET enabled = NOT enabled WHERE rule_key=%s", (rule_key,))
                conn.commit()
            except Exception:
                pass
            finally:
                conn.close()
        if rule_key in self._mem:
            self._mem[rule_key]["enabled"] = not self._mem[rule_key]["enabled"]
