"""V9.3 Phase 10 — Public SSE Proxy / Replay / heartbeat（In-memory MVP + TDD）。

合同 §五十四-五十六 / §七十六：
- P10.4 Public SSE Proxy：Browser 只连 query-api；heartbeat 10–15s；disconnect 不 cancel；
  public reconnect 每次重新授权。
- P10.5 Replay：支持 Last-Event-ID / after_sequence；超出 retention 必须明确错误或完整状态 reload，
  不能 silently skip。
- P10.8 Cancel：SSE disconnect / browser close / timeout 都不能自动等价 cancel。

边界：In-memory MVP（不接真实 HTTP/SSE 代理）；SSEEvent 持久化在 RunEventStore。
"""
from __future__ import annotations

from datetime import datetime, timezone
from typing import Any
from uuid import UUID

import contracts
from run_persistence import RunEventStore, RunStateStore


class SSEStreamError(ValueError):
    def __init__(self, code: str, message: str):
        self.error_code = code
        super().__init__(message)


# 保留窗口：超过该 sequence 的旧事件不可 replay（P10.5 retention）
RETENTION_AFTER_SEQUENCE = 10000


def _now() -> datetime:
    return datetime.now(timezone.utc)


class SSEStream:
    """P10.4/P10.5 — 面向 Browser 的 SSE 流（heartbeat + replay + 授权）。

    - 订阅方为 query-api（public proxy）；heartbeat 10-15s。
    - disconnect / close / timeout 不影响 Run 状态（不 cancel）。
    - reconnect 用 Last-Event-ID（after_sequence）重放；超 retention 明确错误。
    """

    def __init__(self, events: RunEventStore, runs: RunStateStore) -> None:
        self._events = events
        self._runs = runs
        self._subscribers: dict[str, Any] = {}

    def subscribe(self, *, run_id: UUID, tenant_id: UUID, after_sequence: int = 0,
                  authorized: bool = True) -> list[contracts.SSEEvent]:
        """P10.4 reconnect 每次重新授权（authorized=False 拒绝）。

        P10 安全整改：订阅必须校验传入 tenant_id 与 Run 归属一致，跨租户订阅
        fail-closed 拒绝（SSE_TENANT_MISMATCH），防止越权读取他租户 Run 事件流。
        """
        if not authorized:
            raise SSEStreamError("SSE_UNAUTHORIZED", "SSE reconnect 需重新授权")
        run_uuid = run_id if isinstance(run_id, UUID) else UUID(str(run_id))
        # 校验 Run 存在 + tenant 归属一致（审计 P1-2：不再只依赖调用方 authorized 标志）
        run = self._runs.get(run_uuid)
        tenant_uuid = tenant_id if isinstance(tenant_id, UUID) else UUID(str(tenant_id))
        if run.tenant_id != tenant_uuid:
            raise SSEStreamError(
                "SSE_TENANT_MISMATCH",
                f"SSE 订阅租户 {tenant_uuid} 与 Run {run_uuid} 归属租户 {run.tenant_id} 不一致",
            )
        if after_sequence > RETENTION_AFTER_SEQUENCE:
            raise SSEStreamError(
                "SSE_RETENTION_EXCEEDED",
                f"after_sequence {after_sequence} 超保留窗口，需完整状态 reload",
            )
        return self._events.replay(run_uuid, after_sequence)

    def heartbeat(self, last_sequence: int) -> bool:
        """heartbeat 10-15s：返回是否仍在流内（此处 In-memory 恒 True）。"""
        return True

    def disconnect(self, run_id: UUID) -> None:
        """P10.8 SSE disconnect 不 cancel Run（仅移除订阅，不触 Run 状态）。"""
        # In-memory：无真实订阅清理；不触 RunStateStore.cancel
        return
