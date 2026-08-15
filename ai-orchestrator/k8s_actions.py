"""结构化 K8s 生命周期动作: 命令生成 + preflight token + 乐观锁 (工作流 C)

对齐 ongrid `execute_k8s_action`:
- 动作命令生成严格落在 shell_policy.EXEC_WRITE 白名单内 (rollout_restart/scale/
  delete_pod/evict_pod/cordon/uncordon/drain)。
- 两阶段 preflight token: dry-run 预检生成一次性 HMAC token (TTL 5min, 绑定
  参数 sha256 + resourceVersion 乐观锁), 真实写必须带 token 且资源版本未变。
- C2/C3 挂载 (main.py) 在此之上叠加审批 (_require_approver / ApprovalStore)
  与审计 (_audit_log)。
"""
import hashlib
import hmac
import json
import os
import time

from shell_policy import ShellPolicy

ACTIONS = ["rollout_restart", "scale", "delete_pod", "evict_pod", "cordon", "uncordon", "drain"]
ACTION_KINDS = {
    "rollout_restart": ("deployment", "statefulset", "daemonset"),
    "scale": ("deployment", "statefulset"),
    "delete_pod": ("pod",),
    "evict_pod": ("pod",),
    "cordon": ("node",),
    "uncordon": ("node",),
    "drain": ("node",),
}
# 需走审批单的 destructive 动作 (C2): 未通过审批不得执行
DESTRUCTIVE_ACTIONS = ("delete_pod", "evict_pod", "cordon", "drain")

PREFLIGHT_TTL = 300  # 秒

_secret = os.environ.get("INTERNAL_TOKEN", "").encode()


def set_secret(s: str):
    """注入 HMAC 密钥 (INTERNAL_TOKEN, 与 query-api 共享), 由 main.py 挂载时调用。"""
    global _secret
    _secret = (s or "").encode()


def _whitelist(cmd: str) -> tuple:
    """命令白名单校验, 返回 (allowed, category)。"""
    return ShellPolicy().is_whitelisted_for_execute(cmd)


def _run_cmd(cmd: str, timeout: int = 30) -> str:
    """执行命令 (复用 tools.execute_shell 的只读通道, 输出截断 2000 字符)。"""
    from tools import execute_shell
    return execute_shell(cmd, timeout=timeout)


def build_command(action: str, *, kind: str, namespace: str, name: str, **kw) -> str:
    """按动作 schema 生成 kubectl 命令, 严格落在 EXEC_WRITE 白名单内。"""
    if action not in ACTION_KINDS:
        raise ValueError(f"未知动作 {action}")
    if kind not in ACTION_KINDS[action]:
        raise ValueError(f"动作 {action} 不支持 kind={kind}")
    if action == "rollout_restart":
        return f"kubectl rollout restart {kind}/{name} -n {namespace}"
    if action == "scale":
        return f"kubectl scale {kind}/{name} --replicas={int(kw['replicas'])} -n {namespace}"
    if action in ("delete_pod", "evict_pod"):
        grace = int(kw.get("grace_period_seconds", 30))
        return f"kubectl delete pod {name} --grace-period={grace} -n {namespace}"
    if action == "cordon":
        return f"kubectl cordon node {name}"
    if action == "uncordon":
        return f"kubectl uncordon node {name}"
    if action == "drain":
        t = int(kw.get("drain_timeout", 300))
        return f"kubectl drain node {name} --ignore-daemonsets --delete-emptydir-data --timeout={t}s"
    raise ValueError(f"未知动作 {action}")


def _args_sha(action, kind, namespace, name, **kw) -> str:
    """动作参数指纹: 用于绑定 preflight token, 参数任一变化即失效。"""
    payload = json.dumps({"a": action, "k": kind, "ns": namespace, "n": name, **kw}, sort_keys=True)
    return hashlib.sha256(payload.encode()).hexdigest()


def make_preflight_token(action, kind, namespace, name, **kw) -> str:
    """生成一次性 preflight token: 参数 sha256 + 过期时间, HMAC-SHA256 签名。"""
    body = {"sha": _args_sha(action, kind, namespace, name, **kw),
            "exp": int(time.time()) + PREFLIGHT_TTL}
    sig = hmac.new(_secret, json.dumps(body, sort_keys=True).encode(), hashlib.sha256).hexdigest()[:16]
    return json.dumps(body) + "." + sig


def verify_preflight_token(token: str, action, kind, namespace, name, **kw) -> bool:
    """校验 preflight token: 签名 + 未过期 + 参数未变。任一不满足返回 False。"""
    body_s, _, sig = token.rpartition(".")
    try:
        body = json.loads(body_s)
    except ValueError:
        return False
    if body.get("exp", 0) < time.time():
        return False
    if body.get("sha") != _args_sha(action, kind, namespace, name, **kw):
        return False
    expect = hmac.new(_secret, json.dumps(body, sort_keys=True).encode(), hashlib.sha256).hexdigest()[:16]
    return hmac.compare_digest(expect, sig)


def _get_cmd(kind: str, namespace: str, name: str) -> str:
    """读取 resourceVersion 的只读命令 (节点为集群级, 不带 -n)。"""
    cmd = f"kubectl get {kind}/{name} -o jsonpath=\"{{.metadata.resourceVersion}}\""
    if namespace:
        cmd += f" -n {namespace}"
    return cmd


def current_resource_version(kind: str, namespace: str, name: str) -> str:
    """读取当前资源版本 (乐观锁依据)。资源不存在/无权限返回空串。"""
    out = (_run_cmd(_get_cmd(kind, namespace, name)) or "").strip()
    if not out or out == "(no output)":
        return ""
    low = out.lower()
    if "not found" in low or "forbidden" in low or "error" in low:
        return ""
    return out


def preflight(action: str, kind: str, namespace: str, name: str, **kw) -> dict:
    """预检: 白名单校验 → 资源存在性 + resourceVersion → 生成 preflight token。

    返回 {"ok": True, "preflight_token", "resource_version", "command", "category"}
    或 {"ok": False, "error": "..."}。
    """
    try:
        cmd = build_command(action, kind=kind, namespace=namespace, name=name, **kw)
    except ValueError as e:
        return {"ok": False, "error": str(e)}
    allowed, cat = _whitelist(cmd)
    if not allowed:
        return {"ok": False, "error": f"命令不在白名单内: {cmd}"}
    rv = current_resource_version(kind, namespace, name)
    if not rv:
        return {"ok": False, "error": "资源不存在"}
    return {"ok": True, "preflight_token": make_preflight_token(action, kind, namespace, name, **kw),
            "resource_version": rv, "command": cmd, "category": cat}


def execute(action: str, kind: str, namespace: str, name: str, **kw) -> str:
    """执行动作命令: 二次白名单校验后走 tools.execute_shell (输出截断)。"""
    cmd = build_command(action, kind=kind, namespace=namespace, name=name, **kw)
    allowed, cat = _whitelist(cmd)
    if not allowed:
        return f"命令被安全策略拒绝: {cmd}"
    return _run_cmd(cmd)


# ══════════════════════════════════════════════════════════════════
#  C2: 端点核心逻辑 (main.py Mount C2 在 _require_approver 之后调用)
# ══════════════════════════════════════════════════════════════════

class K8sActionError(Exception):
    """带 HTTP 状态码的动作错误 (挂载层映射为 HTTPException)。"""

    def __init__(self, status_code: int = 400, message: str = ""):
        super().__init__(message)
        self.status_code = status_code


def require_approved_task(task_id: str, name: str = "") -> dict:
    """审批门: ApprovalStore 任务必须存在且 status=approved, 且目标资源名在审批 script 内。"""
    from db_approval import ApprovalStore
    task = ApprovalStore().get(task_id or "")
    if not task or task.get("status") != "approved":
        raise K8sActionError(403, "审批未通过或不存在")
    script = task.get("script", "") or ""
    if name and script:
        # 参数匹配: 审批单 script 必须引用目标资源名
        if name not in script:
            raise K8sActionError(403, "审批单参数与目标资源不匹配")
    return task


def execute_guarded(action: str, kind: str, namespace: str, name: str,
                    preflight_token: str = "", expected_resource_version: str = "",
                    approval_task_id: str = "", extra: dict = None,
                    audit=None) -> dict:
    """C2 execute 端点核心: 审批(destructive) → preflight token → 乐观锁 → 执行 → 审计回调。

    - 403: destructive 动作未获审批单批准 (或参数不匹配)
    - 400: preflight_token 缺失/篡改/过期
    - 409: resourceVersion 变化, 需重新预检
    - audit(action, kind, name, output) 由挂载层绑定 _audit_log
    """
    extra = extra or {}
    if action in DESTRUCTIVE_ACTIONS:
        require_approved_task(approval_task_id, name=name)
    if not verify_preflight_token(preflight_token, action, kind, namespace, name, **extra):
        raise K8sActionError(400, "preflight_token 无效或已过期")
    current = current_resource_version(kind, namespace, name)
    if expected_resource_version and current != expected_resource_version:
        raise K8sActionError(409, "资源版本已变化, 请重新预检")
    out = execute(action, kind, namespace, name, **extra)
    if audit:
        audit(action, kind, name, out)
    return {"ok": True, "output": out}
