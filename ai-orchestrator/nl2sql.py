"""NL→ClickHouse SQL：生成-确认-执行 + 安全护栏。

安全规则：
- 只允许 SELECT（拒绝 INSERT/UPDATE/DELETE/DROP/ALTER/CREATE 等）
- 表名必须来自白名单
- 多语句（含分号）拒绝
- 强制追加 LIMIT 护栏
"""
import re
import time
import uuid


_ALLOWED_TABLES = {
    "observability.trace_spans",
    "observability.service_topology",
    "observability.log_records",
    "observability.inspection_reports",
}

_FORBIDDEN_KEYWORDS = re.compile(
    r"\b(insert|update|delete|drop|alter|create|truncate|grant|revoke|"
    r"attach|detach|rename|kill|optimize|system)\b", re.IGNORECASE)


def validate_sql(sql: str, allowed: set = _ALLOWED_TABLES) -> bool:
    """安全校验：只允许 SELECT、表白名单、禁止多语句与非查询关键字。"""
    if not sql or not sql.strip():
        return False
    s = sql.strip()
    # 去掉尾部可选分号后检查，若内部还有分号则为多语句
    body = s[:-1] if s.endswith(";") else s
    if ";" in body:
        return False
    if not re.match(r"^\s*SELECT\s+", body, re.IGNORECASE):
        return False
    if _FORBIDDEN_KEYWORDS.search(body):
        return False
    # 表名白名单：出现的 observability.xxx 必须在允许集合内
    for t in set(re.findall(r"\bobservability\.\w+", body)):
        if t not in allowed:
            return False
    return True


def normalize_sql(sql: str, allowed: set = _ALLOWED_TABLES) -> str:
    """清洗并在缺少 LIMIT 时追加护栏。"""
    if not sql:
        return sql
    s = sql.strip()
    if s.endswith(";"):
        s = s[:-1]
    if not re.search(r"\bLIMIT\b", s, re.IGNORECASE):
        s = s.rstrip() + " LIMIT 100"
    return s


def extract_sql_from_markdown(raw: str) -> str:
    """从可能带 markdown 代码块的 LLM 输出中提取 SQL 本体。"""
    m = re.search(r"```(?:sql)?\s*(.*?)\s*```", raw, re.DOTALL)
    if m:
        return m.group(1).strip()
    return raw.strip()


class Nl2SqlStore:
    """NL→SQL 翻译状态存储（内存，MySQL 可用时可选扩展）。"""

    def __init__(self):
        self._mem: dict[str, dict] = {}

    def save(self, item: dict) -> str:
        item["status"] = "pending"
        self._mem[item["id"]] = item
        return item["id"]

    def get(self, sid: str):
        return self._mem.get(sid)

    def mark_executed(self, sid: str):
        if sid in self._mem:
            self._mem[sid]["status"] = "executed"


def new_item(sql: str, explanation: str) -> dict:
    return {
        "id": uuid.uuid4().hex[:8],
        "sql": sql,
        "explanation": explanation,
        "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
    }
