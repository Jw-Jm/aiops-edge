"""Bounded, idempotent acceptance queue for persistent Investigation Runs."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import Any


@dataclass(frozen=True)
class AcceptedInvocation:
    run_id: str
    invocation_id: str
    request_id: str
    tenant_id: str
    cluster_id: str
    intent: str
    resource_id: str
    service: str
    message: str
    action_mode: str
    request_context: Any = None


@dataclass(frozen=True)
class AcceptResult:
    run_id: str
    invocation_id: str
    accepted: bool
    duplicate: bool = False


class InvestigationDispatcher:
    """Accept work only once, then run it outside the HTTP request lifetime."""

    def __init__(self, runtime: Any, *, capacity: int = 100) -> None:
        if capacity < 1:
            raise ValueError("capacity must be positive")
        self._runtime = runtime
        self._queue: asyncio.Queue[AcceptedInvocation] = asyncio.Queue(maxsize=capacity)
        self._pending: dict[str, AcceptedInvocation] = {}
        self._lock = asyncio.Lock()
        self._workers: list[asyncio.Task] = []
        self._started = False

    async def start(self, workers: int = 1) -> None:
        if self._started:
            return
        if workers < 1:
            raise ValueError("workers must be positive")
        self._started = True
        self._workers = [asyncio.create_task(self._worker()) for _ in range(workers)]

    async def stop(self) -> None:
        tasks, self._workers = self._workers, []
        self._started = False
        for task in tasks:
            task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)

    async def accept(self, item: AcceptedInvocation) -> AcceptResult:
        async with self._lock:
            existing = self._pending.get(item.invocation_id)
            if existing is not None:
                return AcceptResult(item.run_id, item.invocation_id, True, duplicate=True)
            # Reserve capacity before claiming a lease or changing Run state.
            # Queue saturation is an availability response, not a failed
            # investigation that should be persisted as a terminal Run.
            if self._queue.full():
                raise asyncio.QueueFull
            # Runtime.accept is deliberately before enqueue: it claims the lease
            # and persists created→planning before the 202 response is returned.
            accepted_item = await self._runtime.accept(item)
            # A durable terminal Run is an idempotent replay after a response
            # loss or process restart.  It has no executable lease and must not
            # be put back on the worker queue.
            accepted_invocation = getattr(accepted_item, "invocation", accepted_item)
            accepted_status = str(getattr(accepted_item, "status", "") or "")
            if accepted_status in {"success", "partial", "failed", "cancelled", "regressed"}:
                return AcceptResult(item.run_id, item.invocation_id, True, duplicate=True)
            try:
                self._queue.put_nowait(accepted_item)
            except asyncio.QueueFull:
                # A worker may race with this enqueue only by consuming space,
                # so this is defensive. Acceptance has already persisted and
                # must be failed closed if the queue implementation disagrees.
                await self._runtime.reject(accepted_item, reason="DISPATCH_QUEUE_FULL")
                raise
            self._pending[getattr(accepted_invocation, "invocation_id", item.invocation_id)] = accepted_item
            return AcceptResult(item.run_id, item.invocation_id, True)

    async def recover(self, items: list[AcceptedInvocation]) -> None:
        """Requeue unfinished, lease-free work discovered at process startup."""
        for item in items:
            try:
                await self.accept(item)
            except asyncio.QueueFull:
                # Leave the item for the next recovery pass; no data-plane work
                # is started when the durable queue cannot accept it.
                continue

    def queued_count(self, invocation_id: str | None = None) -> int:
        if invocation_id is None:
            return len(self._pending)
        return int(invocation_id in self._pending)

    async def _worker(self) -> None:
        while True:
            item = await self._queue.get()
            try:
                await self._runtime.execute(item)
            except asyncio.CancelledError:
                raise
            except Exception as exc:  # noqa: BLE001
                await self._runtime.fail(item, exc)
            finally:
                # Runtime.accept returns AcceptedWork, while test/durable
                # adapters may return AcceptedInvocation directly. Always
                # remove the durable invocation key from the wrapped value.
                invocation = getattr(item, "invocation", item)
                self._pending.pop(getattr(invocation, "invocation_id", ""), None)
                self._queue.task_done()
