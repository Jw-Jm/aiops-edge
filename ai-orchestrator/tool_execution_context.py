"""Immutable identity passed with every data-plane Tool invocation."""

from __future__ import annotations

import hashlib
import json
import uuid
from dataclasses import dataclass
from typing import Any, Mapping


def _uuid(value: Any, name: str) -> str:
    try:
        return str(uuid.UUID(str(value)))
    except (ValueError, TypeError, AttributeError) as exc:
        raise ValueError(f"{name} must be a UUID") from exc


@dataclass(frozen=True)
class ToolExecutionContext:
    workload_kind: str
    run_id: str
    invocation_id: str
    tenant_id: str
    cluster_id: str
    executor_id: str
    lease_epoch: int
    lease_token: str
    tool_run_id: str
    idempotency_key: str

    @classmethod
    def from_mapping(cls, context: Mapping[str, Any], *, tool_id: str,
                     params: Mapping[str, Any]) -> "ToolExecutionContext":
        kind = str(context.get("workload_kind") or "chat")
        if kind not in {"investigation", "chat", "platform"}:
            raise ValueError("invalid workload_kind")
        if kind != "investigation":
            # Chat/platform are not allowed to silently masquerade as an
            # Investigation; they carry no lease-bound fields.
            return cls(kind, "", "", str(context.get("tenant_id") or ""),
                       str(context.get("cluster_id") or ""), "", 0, "", "", "")
        run_id = _uuid(context.get("run_id"), "run_id")
        invocation_id = _uuid(context.get("invocation_id"), "invocation_id")
        tenant_id = _uuid(context.get("tenant_id"), "tenant_id")
        cluster_id = _uuid(context.get("cluster_id"), "cluster_id")
        executor_id = str(context.get("executor_id") or "")
        lease_token = str(context.get("lease_token") or "")
        try:
            lease_epoch = int(context.get("lease_epoch"))
        except (TypeError, ValueError) as exc:
            raise ValueError("lease_epoch must be positive") from exc
        if not executor_id or lease_epoch <= 0 or not lease_token:
            raise ValueError("investigation Tool requires active lease identity")
        tool_run_id = str(context.get("tool_run_id") or uuid.uuid4())
        _uuid(tool_run_id, "tool_run_id")
        raw = json.dumps({"tool_id": tool_id, "params": dict(params)}, sort_keys=True,
                         separators=(",", ":"), default=str).encode()
        digest = hashlib.sha256(raw).hexdigest()
        idempotency_key = str(context.get("idempotency_key") or f"{invocation_id}:{tool_id}:{digest}")
        return cls(kind, run_id, invocation_id, tenant_id, cluster_id, executor_id,
                   lease_epoch, lease_token, tool_run_id, idempotency_key)

    def to_body(self) -> dict[str, Any]:
        if self.workload_kind != "investigation":
            return {"workload_kind": self.workload_kind}
        return {
            "workload_kind": self.workload_kind,
            "run_id": self.run_id,
            "tool_run_id": self.tool_run_id,
            "idempotency_key": self.idempotency_key,
            "executor_id": self.executor_id,
            "lease_epoch": self.lease_epoch,
            "lease_token": self.lease_token,
        }
