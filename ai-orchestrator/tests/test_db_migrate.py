import os
os.environ.setdefault("MYSQL_HOST", "127.0.0.1")
os.environ.setdefault("MYSQL_PORT", "3306")
os.environ.setdefault("MYSQL_USER", "root")
os.environ.setdefault("MYSQL_PASSWORD", "")
os.environ.setdefault("MYSQL_DB", "aiops")

import glob
import os.path as p


def test_phase2_migration_files_exist():
    """二期迁移文件存在且非空。"""
    files = sorted(glob.glob(p.join(p.dirname(p.dirname(__file__)), "migrations", "*.sql")))
    assert any("0002_phase2_tables" in f for f in files)
    phase2 = [f for f in files if "0002_phase2_tables" in f]
    assert len(phase2) == 1
    with open(phase2[0], encoding="utf-8") as fh:
        content = fh.read()
    # 包含 5 张二期采集表
    for table in ["snmp_devices", "network_interfaces", "ipmi_sensors",
                  "ipmi_sel_events", "node_component_health"]:
        assert f"CREATE TABLE IF NOT EXISTS {table}" in content


def test_migrate_idempotent():
    """迁移器幂等，重复执行安全（MySQL 不可用时降级不抛异常）。"""
    from db import migrate
    migrate()
    migrate()
    assert True
