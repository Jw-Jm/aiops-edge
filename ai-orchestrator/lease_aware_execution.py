"""B2-01（0004_runtime_convergence）：Lease-aware main loop。

orchestrator run dispatch 执行前 Claim Run execution lease（epoch+token fencing），
执行期间周期 renew，执行完成（成功/失败/终态）后 Runtime Commit（Lease fencing + Run CAS
+ 事件追加 + commit 记录原子）。重启后 Recovery Scanner 只恢复无活跃 Lease 的 Run，
避免双 executor 抢同一活跃 Run。

调用方：run 派发主循环（PersistentRunRepository / run dispatch）。当 query-api
control-plane lease/commit 端点不可达时 fail-closed（不执行无 Lease 保护的 Run）。
"""
from __future__ import annotations

import hashlib
import json
import uuid
from datetime import datetime, timedelta, timezone
from typing import Any, Callable, Optional

from control_plane_client import ControlPlaneClient, ControlPlaneError

DEFAULT_LEASE_SECONDS = 60
RENEW_INTERVAL_SECONDS = 25
SYSTEM_OWNER = "orchestrator-run-loop"


class LeaseAcquireError(RuntimeError):
    """无法取得/保持 Run lease（fail-closed，不执行无 Lease 保护的 Run）。"""


class LeaseLostError(RuntimeError):
    """执行期间 Lease 丢失/不确定（P0#4/#12：ACTIVE→UNCERTAIN→LOST 停止规则）。

    调用方在进入下一个数据面访问/动作前应停止，不执行无 Lease 保护的动作。
    """


class _LeaseState:
    """Lease 三态（P0#4/#12）：ACTIVE（可继续）→ UNCERTAIN（renew 失败，暂缓）→ LOST（停止）。"""

    ACTIVE = "active"
    UNCERTAIN = "uncertain"
    LOST = "lost"


class LeaseAwareExecutor:
    """把一次 Run 执行包装为 Claim→execute→(renew loop)→Commit 的 Lease 边界。

    用法：
        executor = LeaseAwareExecutor(client)
        with executor.lease(run_id, tenant_id) as lease:
            result = await run_fn()   # 执行期间后台线程周期 renew
        # with 退出时：成功 → Commit；异常 → 记 failed；超时未续 → 不 commit
    """

    def __init__(self, client: Optional[ControlPlaneClient] = None) -> None:
        self._client = client or ControlPlaneClient()

    def _claims(self, run_id: str, tenant_id: str) -> dict:
        return {"run_id": run_id, "tenant_id": tenant_id}

    def lease(self, run_id: str, tenant_id: str, owner_id: str = SYSTEM_OWNER,
              lease_seconds: int = DEFAULT_LEASE_SECONDS):
        """返回 LeaseContext 上下文管理器。"""
        return _LeaseContext(self._client, run_id, tenant_id, owner_id, lease_seconds)

    def acquire(self, *, run_id: str, tenant_id: str, owner_id: str = SYSTEM_OWNER,
                claim_id: str = "", lease_seconds: int = DEFAULT_LEASE_SECONDS):
        """Acquire and return a handle whose lifetime can span HTTP acceptance.

        The caller must invoke ``close`` exactly once after queued work has
        completed. This is the asynchronous counterpart to ``lease(...)``.
        """
        handle = _LeaseContext(
            self._client, run_id, tenant_id, owner_id, lease_seconds,
            claim_id=claim_id or None,
        )
        handle.__enter__()
        return handle


class _LeaseContext:
    def __init__(self, client: ControlPlaneClient, run_id: str, tenant_id: str,
                 owner_id: str, lease_seconds: int, claim_id: Optional[str] = None) -> None:
        self._client = client
        self._run_id = run_id
        self._tenant_id = tenant_id
        self._owner_id = owner_id
        self._lease_seconds = lease_seconds
        self._epoch: Optional[int] = None
        self._token: str = ""
        self._renew_thread: Optional[Any] = None
        self._stop = False
        self._state = _LeaseState.ACTIVE  # P0#4/#12：Lease 三态
        self._commit_id: Optional[str] = None  # P0#12：稳定 commit_id（重试复用）
        self._renew_failures = 0
        # P0-LEASE-03：caller 生成稳定 claim_id + lease_token（>=256-bit random），
        # Claim 响应丢失后以相同 claim_id 精确重试恢复同一 Lease。
        self._claim_id = claim_id or str(uuid.uuid4())
        self._lease_token = str(uuid.uuid4()) + str(uuid.uuid4())

    def __enter__(self) -> "_LeaseContext":
        holder = self._client.claim_lease(
            run_id=self._run_id, tenant_id=self._tenant_id,
            owner_id=self._owner_id, lease_seconds=self._lease_seconds,
            claim_id=self._claim_id, lease_token=self._lease_token,
        )
        self._epoch = int(holder.get("epoch", 0))
        # P0-LEASE-03：服务端返回明文 token（= caller 提供的 lease_token，claim 成功后一致）。
        self._token = str(holder.get("token", "") or self._lease_token)
        if not self._epoch or not self._token:
            raise LeaseAcquireError("claim lease: missing epoch/token")
        self._start_renew_loop()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self._stop = True
        if self._renew_thread is not None:
            self._renew_thread.join(timeout=3)
        return False  # 不吞异常

    def close(self) -> None:
        self.__exit__(None, None, None)

    # P0#4/#12：Lease 状态机——每次 renew 后更新 ACTIVE；renew 失败 → UNCERTAIN → LOST。
    # LOST 状态下调用方应停止（不执行无 Lease 保护的数据面/动作）。
    @property
    def state(self) -> str:
        return self._state

    @property
    def lease_lost(self) -> bool:
        return self._state == _LeaseState.LOST

    def check_active(self) -> None:
        """在进入数据面访问/动作前调用：Lease 非 ACTIVE → 抛 LeaseLostError（停止规则）。"""
        if self._state != _LeaseState.ACTIVE:
            raise LeaseLostError(f"lease {self._state} for run {self._run_id}; stop before data-plane access")

    # ── renew loop（后台线程周期续约，防 Lease 过期被回收）────────────────
    def _start_renew_loop(self) -> None:
        import threading
        self._renew_thread = threading.Thread(target=self._renew_loop, daemon=True)
        self._renew_thread.start()

    def _renew_loop(self) -> None:
        import time
        while not self._stop:
            time.sleep(RENEW_INTERVAL_SECONDS)
            if self._stop:
                break
            if self._state == _LeaseState.LOST:
                break
            try:
                self._client.renew_lease(
                    run_id=self._run_id, tenant_id=self._tenant_id,
                    owner_id=self._owner_id, epoch=int(self._epoch or 0),
                    token=self._token, lease_seconds=self._lease_seconds,
                )
                self._state = _LeaseState.ACTIVE
                self._renew_failures = 0
            except ControlPlaneError:
                # renew 失败：Lease 可能已过期/被回收。
                # 停止规则（P0#4/#12）：第一次失败 ACTIVE→UNCERTAIN（暂缓，仍尝试继续），
                # 连续两次失败 → LOST（停止，不执行无 Lease 保护的数据面/动作）。
                self._renew_failures += 1
                if self._renew_failures >= 2:
                    self._state = _LeaseState.LOST
                elif self._state == _LeaseState.ACTIVE:
                    self._state = _LeaseState.UNCERTAIN
                if self._state == _LeaseState.LOST:
                    break

    # ── Commit（执行完成：成功推进 target；失败记 failed）────────────────
    def commit(self, *, target: str, result: dict, events: list,
               expected_version: int, payload: Any = None) -> dict:
        """原子 Runtime Commit（P0#12：commit_id 稳定——同一次执行的重试复用同一 commit_id，
        幂等返回首次结果；不因重试生成新 commit_id）。"""
        payload_hash = _sha256(payload if payload is not None else result)
        if self._commit_id is None:
            self._commit_id = str(uuid.uuid4())
        return self._client.commit(
            run_id=self._run_id, tenant_id=self._tenant_id, commit_id=self._commit_id,
            payload_hash=payload_hash, target=target, result=result, events=events,
            expected_version=expected_version, owner_id=self._owner_id,
            epoch=int(self._epoch or 0), token=self._token,
        )


def _sha256(obj: Any) -> str:
    return hashlib.sha256(json.dumps(obj, sort_keys=True, default=str).encode("utf-8")).hexdigest()
