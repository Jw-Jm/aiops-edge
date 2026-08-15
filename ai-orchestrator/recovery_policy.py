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
        "kubectl scale",
        "kubectl delete pod",
        "systemctl restart",
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
    """检查恢复命令是否在白名单内。返回 (是否允许, 原因)。

    安全修复(P0-5): 此前仅 startswith 前缀匹配 allow 列表 + deny 子串，攻击者可构造
    `kubectl rollout restart deploy/x; cat /etc/shadow` 这类"白名单前缀 + 拼接"绕过。
    现在复用 shell_policy 完整校验：① check_shell_metachars；② is_whitelisted_for_execute；
    ③ 保留 deny 列表；全部通过才算 allowed。保持函数签名不变。
    """
    if not command or not command.strip():
        return False, "命令为空"
    cmd = command.strip()
    policy = get_policy()
    allow = policy.get("allow", [])
    deny = policy.get("deny", [])
    # ③ 先查拒绝列表（更强）
    for d in deny:
        if d.lower() in cmd.lower():
            return False, f"命中禁止列表: {d}"
    # ①② 复用 shell_policy 完整校验：元字符拦截 + 执行白名单（含危险参数黑名单）
    try:
        from shell_policy import ShellPolicy
        sp = ShellPolicy()
        if mc := sp.check_shell_metachars(cmd):
            return False, f"命令含禁止的 shell 元字符: {mc}"
        ok, category = sp.is_whitelisted_for_execute(cmd)
        if not ok:
            return False, f"命令不在可执行白名单内: {category}"
    except Exception as e:
        return False, f"安全策略校验失败: {e}"
    # 再查恢复白名单 allow 前缀（须命中恢复白名单，且已通过上述完整校验）
    for a in allow:
        if cmd.lower().startswith(a.lower()):
            return True, f"命中白名单: {a}"
    return False, "不在恢复白名单内"
