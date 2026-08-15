"""NL→ClickHouse SQL：生成-确认-执行 + 安全护栏。

安全规则：
- 只允许 SELECT（拒绝 INSERT/UPDATE/DELETE/DROP/ALTER/CREATE 等）
- 表名必须来自白名单（且必须带库前缀，拒绝裸表名）
- 禁止 ClickHouse 表函数（file/url/remote/mysql/s3 等 → SSRF/出网/落盘/CPU 放大）
- 禁止 INTO OUTFILE（任意落盘）
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
}

_FORBIDDEN_KEYWORDS = re.compile(
    r"\b(insert|update|delete|drop|alter|create|truncate|grant|revoke|"
    r"attach|detach|rename|kill|optimize|system)\b", re.IGNORECASE)

# P1-1 加固: ClickHouse 表函数可导致 SSRF(url/remote/jdbc)、任意出网(s3/hdfs/gcs)、
# 任意文件读取(file)、CPU 放大(numbers/generateRandom)、跨库读写(mysql/postgresql/mongodb)。
# 用 \b(keyword)\s*\( 匹配（大小写不敏感），仅在作为函数调用时拒绝。
_TABLE_FUNCTION_KEYWORDS = (
    "file", "url", "remote", "remoteSecure", "mysql", "postgresql", "mongodb",
    "jdbc", "s3", "hdfs", "gcs", "numbers", "generateRandom",
)
_TABLE_FUNCTION_RE = re.compile(
    r"\b(" + "|".join(_TABLE_FUNCTION_KEYWORDS) + r")\s*\(", re.IGNORECASE)

# P1-1 加固: INTO OUTFILE 会把查询结果落盘到任意路径（写入攻击面）。
_INTO_OUTFILE_RE = re.compile(r"\bINTO\s+OUTFILE\b", re.IGNORECASE)

# P1-1 加固: FROM/JOIN 后的表引用必须带库前缀（db.table），裸表名（FROM 表名）一律拒绝，
# 防止绕过硬白名单读取默认库/未知库表。同时拒绝反引号/引号包裹的标识符引用
# （``FROM `trace_spans` `` 可绕过上述基于字符的白名单匹配）。
_BARE_TABLE_RE = re.compile(
    r"\b(?:FROM|JOIN)\s+([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)?)", re.IGNORECASE)
_QUOTED_TABLE_RE = re.compile(r"\b(?:FROM|JOIN)\s+[`'\"]", re.IGNORECASE)


def validate_sql(sql: str, allowed: set = _ALLOWED_TABLES) -> bool:
    """安全校验：只允许 SELECT、表白名单（带库前缀）、禁止多语句/非查询关键字/表函数/INTO OUTFILE。"""
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
    # P1-1: 表函数（SSRF/出网/落盘/CPU 放大）与 INTO OUTFILE（落盘）一律拒绝
    if _TABLE_FUNCTION_RE.search(body):
        return False
    if _INTO_OUTFILE_RE.search(body):
        return False
    # P1-1: FROM/JOIN 后的表引用必须带库前缀；反引号/引号包裹的标识符引用一并拒绝
    if _QUOTED_TABLE_RE.search(body):
        return False
    for m in _BARE_TABLE_RE.finditer(body):
        if "." not in m.group(1):
            return False
    # 表名白名单：SQL 中出现的所有 "库.表" 引用，必须且只能命中允许集合。
    # 禁止任何非 observability 库的表（如 system.*、default.*、其他库），
    # 防止越权读取集群元数据/未知表。
    for t in set(re.findall(r"\b[a-zA-Z_][a-zA-Z0-9_]*\.[a-zA-Z_][a-zA-Z0-9_]*", body)):
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
