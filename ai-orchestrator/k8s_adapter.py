"""PE.1 K8sAdapter — 真实执行适配器，独立实现 Adapter Interface v1（不继承 MockAdapter）。

内部复用 k8s_actions 真实引擎（preflight token + 白名单 + 资源版本乐观锁）。
仅实现 Adapter Interface v1 的 execute/dry_run/verify_contract_scope。
"""
from __future__ import annotations

import uuid
from datetime import datetime, timezone
from typing import Any, Dict, Optional

from execution_adapter import AdapterRequest, AdapterResult
from execution_contract import ExecutionContract

# 适配 Adapter Interface v1 的 action 名 ↔ k8s_actions ACTIONS
_ALLOWED_ACTIONS = {"rollout_restart", "scale"}
_FORBIDDEN_ACTIONS = {"delete_pod", "evict_pod", "cordon", "uncordon", "drain", "create", "delete"}


class K8sAdapter:
    def __init__(self, *, adapter_id: str) -> None:
        self.adapter_id = adapter_id
        self._executed: Dict[str, AdapterResult] = {}  # R4.1

    def verify_contract_scope(self, request: AdapterRequest, contract: ExecutionContract) -> bool:
        if contract.status != "active":
            return False
        if datetime.now(timezone.utc) > contract.expire_time:
            return False
        if request.action in _FORBIDDEN_ACTIONS or request.action not in _ALLOWED_ACTIONS:
            return False
        if request.action not in contract.allowed_actions:
            return False
        ns = (request.target or {}).get("namespace", "")
        if ns and ns not in contract.allowed_resources:
            return False
        return True

    def dry_run(self, request: AdapterRequest, contract: ExecutionContract) -> AdapterResult:
        if not self.verify_contract_scope(request, contract):
            return self._denied("scope/action 校验失败")
        if not request.credential_ref:
            return self._denied("无有效 credential_ref")
        kind = request.target.get("kind", "deployment")
        ns = request.target.get("namespace", "")
        name = request.target.get("resource_id", "")
        import k8s_actions
        pf = k8s_actions.preflight(request.action, kind, ns, name, **request.params)
        if not pf.get("ok"):
            return self._denied(f"preflight 失败: {pf.get('error')}", status="dry_run")
        return AdapterResult(
            status="dry_run",
            output={"action": request.action, "preview": True, "command": pf.get("command"), "resource_version": pf.get("resource_version")},
            adapter_id=self.adapter_id,
            target_snapshot=dict(request.target),
            execution_trace_id=str(uuid.uuid4()),
        )

    def execute(self, request: AdapterRequest, contract: ExecutionContract) -> AdapterResult:
        if not self.verify_contract_scope(request, contract):
            return self._denied("scope/action 校验失败（contract 非 active / 白名单外 / 资源范围外 / 禁绝动作）")
        if not request.credential_ref:
            return self._denied("无有效 credential_ref")
        if request.idempotency_key and request.idempotency_key in self._executed:
            return self._executed[request.idempotency_key]
        if request.dry_run:
            return self._denied("dry_run 应走 dry_run()", status="dry_run")
        import k8s_actions
        kind = request.target.get("kind", "deployment")
        ns = request.target.get("namespace", "")
        name = request.target.get("resource_id", "")
        before_rv = k8s_actions.current_resource_version(kind, ns, name)
        out = k8s_actions.execute(request.action, kind, ns, name, **request.params)
        after_rv = k8s_actions.current_resource_version(kind, ns, name)
        trace_id = str(uuid.uuid4())
        result = AdapterResult(
            status="success" if "拒绝" not in out else "failed",
            output={"action": request.action, "target": request.target, "raw": out},
            rollback_ref=f"rollback::{trace_id}",
            executed_at=datetime.now(timezone.utc),
            adapter_id=self.adapter_id,
            target_snapshot=dict(request.target),
            before_state={"resource_version": before_rv},
            after_state={"resource_version": after_rv},
            execution_trace_id=trace_id,
            contract_permission_snapshot={"contract_id": contract.contract_id, "allowed_actions": list(contract.allowed_actions), "allowed_resources": list(contract.allowed_resources), "max_scope": contract.max_scope},
        )
        if request.idempotency_key:
            self._executed[request.idempotency_key] = result
        return result

    def _denied(self, reason: str, status: str = "denied") -> AdapterResult:
        return AdapterResult(status=status, reason=reason, adapter_id=self.adapter_id, execution_trace_id=str(uuid.uuid4()))
