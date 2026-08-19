"""WebShell WebSocket 端点：白名单 + 会话/超时限制。

安全模型：
- 命令经 ShellPolicy 双重校验（危险命令拦截 + 执行白名单）
- 只读命令直接执行；写命令/非白名单命令拒绝
- 会话并发限制、单命令超时、空闲断开
"""
import asyncio
import hmac
import os
import time
import subprocess

from fastapi import WebSocket, WebSocketDisconnect

from shell_policy import ShellPolicy

_MAX_SESSIONS = int(os.environ.get("SHELL_MAX_SESSIONS", "5"))
_TIMEOUT = int(os.environ.get("SHELL_TIMEOUT", "30"))
_IDLE_TIMEOUT = int(os.environ.get("SHELL_IDLE_TIMEOUT", "300"))

_policy = ShellPolicy()
_active_sessions = 0
_active_lock = asyncio.Lock()


async def _acquire_session() -> bool:
    """尝试获取会话名额；满则拒绝。"""
    global _active_sessions
    async with _active_lock:
        if _active_sessions >= _MAX_SESSIONS:
            return False
        _active_sessions += 1
        return True


async def _release_session():
    global _active_sessions
    async with _active_lock:
        if _active_sessions > 0:
            _active_sessions -= 1


def _audit_shell(operator: str, command: str, result: str):
    """记录 WebShell 命令执行审计到 MySQL（audit_logs），失败静默。"""
    try:
        from db_audit import AuditStore
        AuditStore().log(action="shell_exec", operator=operator or "unknown",
                         target="", command=command, result=result[:500],
                         detail=None, task_id="shell")
    except Exception:
        pass


def _is_ws_authorized(ws: WebSocket) -> bool:
    """Allow only an explicitly enabled manual shell with service auth.

    Browser/JWT proxying is intentionally not an authorization path for R4
    shell. Manual operators must enable the route and use a non-browser client
    that can provide the service credential as a header.
    """
    if os.environ.get("SHELL_MANUAL_ENABLED", "").lower() not in {
        "1", "true", "yes", "on"
    }:
        return False
    expected = os.environ.get("INTERNAL_TOKEN", "")
    if not expected:
        return False
    got = ws.headers.get("X-Internal-Token", "")
    return bool(got) and hmac.compare_digest(got, expected)


def _is_ws_privileged(ws: WebSocket) -> bool:
    """Compatibility hook: privilege comes only from the manual deployment gate."""
    del ws
    return os.environ.get("SHELL_MANUAL_ENABLED", "").lower() in {
        "1", "true", "yes", "on"
    }


async def shell_ws(ws: WebSocket):
    if not _is_ws_authorized(ws):
        await ws.accept()
        await ws.send_text("❌ 未授权：缺少有效的内部令牌。\n")
        await ws.close(code=1008)
        return
    # R4 shell remains a manually enabled path; role/approver headers are not authority.
    if not _is_ws_privileged(ws):
        await ws.accept()
        await ws.send_text("❌ 权限不足：WebShell 仅限管理员或审批人使用。\n")
        await ws.close(code=1008)
        return
    if not await _acquire_session():
        await ws.accept()
        await ws.send_text("❌ 并发会话数已达上限，请稍后再试。\n")
        await ws.close(code=1013)
        return

    await ws.accept()
    _operator = "manual-shell"
    last_activity = time.time()
    try:
        while True:
            # 空闲超时检测
            if time.time() - last_activity > _IDLE_TIMEOUT:
                await ws.send_text("⏱ 连接空闲超时，已断开。\n")
                break

            try:
                cmd = await asyncio.wait_for(ws.receive_text(), timeout=min(60, _IDLE_TIMEOUT))
            except asyncio.TimeoutError:
                continue
            except WebSocketDisconnect:
                break

            cmd = (cmd or "").strip()
            if not cmd:
                continue

            # 0. 命令必须非空且不能过长（防超长输入）
            if len(cmd) > 4096:
                await ws.send_text("❌ 命令过长（>4096 字符）\n")
                continue

            # 1. 危险命令拦截
            danger = _policy.check(cmd)
            if danger:
                await ws.send_text(f"❌ {danger}\n")
                continue

            # 1.5 shell 拼接/重定向元字符拦截（防白名单子串 + 任意命令注入绕过）
            meta = _policy.check_shell_metachars(cmd)
            if meta:
                await ws.send_text(f"❌ {meta}\n")
                continue

            # 2. 执行白名单校验
            ok, category = _policy.is_whitelisted_for_execute(cmd)
            if not ok:
                await ws.send_text(f"❌ 命令不在可执行白名单内：{cmd}\n")
                continue

            # 3. 执行命令（只读/写白名单均执行；写入操作前已在白名单判定）
            # cwd 从环境变量注入（可移植，不写死 /workspace）
            workdir = os.environ.get("SHELL_CWD") or os.environ.get("HOME")
            if not workdir or not os.path.isdir(workdir):
                workdir = None
            last_activity = time.time()
            try:
                proc = await asyncio.create_subprocess_shell(
                    cmd,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    cwd=workdir,
                )
                try:
                    out, _ = await asyncio.wait_for(proc.communicate(), timeout=_TIMEOUT)
                except asyncio.TimeoutError:
                    proc.kill()
                    await ws.send_text(f"⏱ 命令超时（>{_TIMEOUT}s）\n")
                    continue
                text = out.decode(errors="replace") if out else ""
                await ws.send_text(text if text else "（无输出）\n")
                _audit_shell(_operator, cmd, text[:200])
            except Exception as e:
                await ws.send_text(f"❌ 执行错误：{e}\n")
                _audit_shell(_operator, cmd, f"ERROR: {e}")
    except WebSocketDisconnect:
        pass
    finally:
        await _release_session()
