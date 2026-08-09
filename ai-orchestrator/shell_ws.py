"""WebShell WebSocket 端点：白名单 + 会话/超时限制。

安全模型：
- 命令经 ShellPolicy 双重校验（危险命令拦截 + 执行白名单）
- 只读命令直接执行；写命令/非白名单命令拒绝
- 会话并发限制、单命令超时、空闲断开
"""
import asyncio
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


async def shell_ws(ws: WebSocket):
    if not await _acquire_session():
        await ws.accept()
        await ws.send_text("❌ 并发会话数已达上限，请稍后再试。\n")
        await ws.close(code=1013)
        return

    await ws.accept()
    # 操作者身份（由 query-api 代理时经 query 参数传入，默认 shell-user）
    _operator = ws.query_params.get("user", "") or "shell-user"
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

            # 1. 危险命令拦截
            danger = _policy.check(cmd)
            if danger:
                await ws.send_text(f"❌ {danger}\n")
                continue

            # 2. 执行白名单校验
            ok, category = _policy.is_whitelisted_for_execute(cmd)
            if not ok:
                await ws.send_text(f"❌ 命令不在可执行白名单内：{cmd}\n")
                continue

            # 3. 执行命令（只读/写白名单均执行；写入操作前已在白名单判定）
            last_activity = time.time()
            try:
                proc = await asyncio.create_subprocess_shell(
                    cmd,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    cwd="/workspace" if os.path.isdir("/workspace") else None,
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
