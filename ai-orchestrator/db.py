"""MySQL 连接池 + 轻量版本化迁移器 + 降级探测。"""
import os
from dbutils.pooled_db import PooledDB
import pymysql

_MYSQL_HOST = os.environ.get("MYSQL_HOST", "127.0.0.1")
_MYSQL_PORT = int(os.environ.get("MYSQL_PORT", "3306"))
_MYSQL_USER = os.environ.get("MYSQL_USER", "root")
_MYSQL_PASSWORD = os.environ.get("MYSQL_PASSWORD", "")
_MYSQL_DB = os.environ.get("MYSQL_DB", "aiops")

_pool = None


def _get_pool():
    global _pool
    if _pool is None:
        _pool = PooledDB(
            creator=pymysql, host=_MYSQL_HOST, port=_MYSQL_PORT,
            user=_MYSQL_USER, password=_MYSQL_PASSWORD, database=_MYSQL_DB,
            charset="utf8mb4", autocommit=False, maxconnections=10,
            cursorclass=pymysql.cursors.DictCursor,
        )
    return _pool


def get_conn():
    """获取连接。失败时返回 None，调用方降级为内存。"""
    try:
        return _get_pool().connection()
    except Exception:
        return None


def db_available() -> bool:
    conn = get_conn()
    if conn is None:
        return False
    try:
        with conn.cursor() as cur:
            cur.execute("SELECT 1")
        conn.close()
        return True
    except Exception:
        conn.close()
        return False


def migrate():
    """顺序执行 migrations/*.sql 中未应用的版本。幂等。"""
    import glob
    conn = get_conn()
    if conn is None:
        return False
    try:
        with conn.cursor() as cur:
            cur.execute(
                "CREATE TABLE IF NOT EXISTS schema_migrations "
                "(version VARCHAR(64) PRIMARY KEY, applied_at DATETIME DEFAULT CURRENT_TIMESTAMP)"
            )
            cur.execute("SELECT version FROM schema_migrations")
            applied = {r["version"] for r in cur.fetchall()}
            conn.commit()
            for path in sorted(glob.glob(os.path.join(os.path.dirname(__file__), "migrations", "*.sql"))):
                version = os.path.basename(path).split("_")[0]
                if version in applied:
                    continue
                with open(path, encoding="utf-8") as f:
                    for stmt in f.read().split(";"):
                        if stmt.strip():
                            cur.execute(stmt)
                cur.execute("INSERT INTO schema_migrations (version) VALUES (%s)", (version,))
                conn.commit()
        return True
    except Exception:
        try:
            conn.rollback()
        except Exception:
            pass
        return False
    finally:
        conn.close()
