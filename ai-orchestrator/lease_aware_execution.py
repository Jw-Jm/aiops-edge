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


class _LeaseContext:
    def __init__(self, client: ControlPlaneClient, run_id: str, tenant_id: str,
                 owner_id: str, lease_seconds: int) -> None:
        self._client = client
        self._run_id = run_id
        self._tenant_id = tenant_id
        self._owner_id = owner_id
        self._lease_seconds = lease_seconds
        self._epoch: Optional[int] = None
        self._token: str = ""
        self._renew_thread: Optional[Any] = None
        self._stop = False

    def __enter__(self) -> "_LeaseContext":
        holder = self._client.claim_lease(
            run_id=self._run_id, tenant_id=self._tenant_id,
            owner_id=self._owner_id, lease_seconds=self._lease_seconds,
        )
        self._epoch = int(holder.get("epoch", 0))
        self._token = str(holder.get("token", ""))
        if not self._epoch or not self._token:
            raise LeaseAcquireError("claim lease: missing epoch/token")
        self._start_renew_loop()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self._stop = True
        if self._renew_thread is not None:
            self._renew_thread.join(timeout=3)
        return False  # 不吞异常

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
            try:
                self._client.renew_lease(
                    run_id=self._run_id, tenant_id=self._tenant_id,
                    owner_id=self._owner_id, epoch=int(self._epoch or 0),
                    token=self._token, lease_seconds=self._lease_seconds,
                )
            except ControlPlaneError:
                # renew 失败：Lease 可能已过期/被回收。标记不再续，让最终 commit 尝试 fenced。
                break

    # ── Commit（执行完成：成功推进 target；失败记 failed）────────────────
    def commit(self, *, target: str, result: dict, events: list,
               expected_version: int, payload: Any = None) -> dict:
        """原子 Runtime Commit（幂等：同 commit_id 重试返回首次结果）。"""
        payload_hash = _sha256(payload if payload is not None else result)
        commit_id = str(uuid.uuid4())
        return self._client.commit(
            run_id=self._run_id, tenant_id=self._tenant_id, commit_id=commit_id,
            payload_hash=payload_hash, target=target, result=result, events=events,
            expected_version=expected_version, owner_id=self._owner_id,
            epoch=int(self._epoch or 0), token=self._token,
        )


def _sha256(obj: Any) -> str:
    return hashlib.sha256(json.dumps(obj, sort_keys=True, default=str).encode("utf-8")).hexdigest()
