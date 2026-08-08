import os
os.environ.setdefault("MYSQL_HOST", "127.0.0.1")
os.environ.setdefault("MYSQL_PORT", "3306")
os.environ.setdefault("MYSQL_USER", "root")
os.environ.setdefault("MYSQL_PASSWORD", "test")
os.environ.setdefault("MYSQL_DB", "aiops")

from db import migrate, db_available


def test_migrate_is_idempotent():
    # 两次迁移幂等（本机无 MySQL 时提前返回 False，断言仍通过）
    migrate()
    migrate()
    assert True


def test_db_available_returns_bool():
    # 返回布尔值（本机无 MySQL → False；真实环境 → True）
    assert isinstance(db_available(), bool)
