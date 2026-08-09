"""恢复白名单策略（安全边界）。

审批制恢复执行前，恢复命令必须命中白名单；白名单可在设置中配置，默认含安全边界。
存储：MySQL platform_settings 表的 `recovery_policy` 键（JSON），失败降级为默认白名单。
"""
import json

from db import get_conn

_SETTING_KEY = "recovery_policy"

# 默认安全边界：允许安全恢复操作，禁止删除/清数据等危险操作。
DEFAULT_POLICY = {
    "allow": [
        "kubectl rollout restart",
        "kubectl rollout undo",
        "kubectl scale --replicas=",
        "kubectl delete pod",
        "systemctl restart",
        "curl -X POST",
    ],
    "deny": [
        "kubectl delete namespace",
        "kubectl delete deployment",
        "kubectl delete service",
        "kubectl delete pvc",
        "rm -rf",
        "DROP TABLE",
        "DROP DATABASE",
        "DELETE FROM",
        "TRUNCATE",
        "format",
    ],
}


def _default():
    return json.loads(json.dumps(DEFAULT_POLICY))


def get_policy() -> dict:
    """读取恢复白名单（MySQL，失败降级默认）。"""
    conn = get_conn()
    if conn is not None:
        try:
            with conn.cursor() as cur:
                cur.execute("SELECT value FROM platform_settings WHERE config_key=%s", (_SETTING_KEY,))
                row = cur.fetchone()
            conn.close()
            if row and row.get("value"):
                return json.loads(row["value"])
        except Exception:
            conn.close()
    return _default()


def set_policy(policy: dict) -> bool:
    """保存恢复白名单（MySQL）。"""
    conn = get_conn()
    if conn is None:
        return False
    try:
        value = json.dumps(policy)
        with conn.cursor() as cur:
            cur.execute(
                "INSERT INTO platform_settings (config_key, value) VALUES (%s, %s) "
                "ON DUPLICATE KEY UPDATE value=VALUES(value)",
                (_SETTING_KEY, value),
            )
        conn.commit()
        conn.close()
        return True
    except Exception:
        try:
            conn.close()
        except Exception:
            pass
        return False


def check_allowed(command: str) -> tuple:
    """检查恢复命令是否在白名单内。返回 (是否允许, 原因)。"""
    if not command or not command.strip():
        return False, "命令为空"
    cmd = command.strip()
    policy = get_policy()
    allow = policy.get("allow", [])
    deny = policy.get("deny", [])
    # 先查拒绝列表（更强）
    for d in deny:
        if d.lower() in cmd.lower():
            return False, f"命中禁止列表: {d}"
    # 再查允许列表
    for a in allow:
        if a.lower() in cmd.lower():
            return True, f"命中白名单: {a}"
    return False, "不在恢复白名单内"
