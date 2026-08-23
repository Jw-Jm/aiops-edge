"""P7.4 Evidence Hub — V9.3 Phase7 统一证据对象（只记录不判断）。

铁律（F3）：
- LLM inference 绝不作为 Evidence（拒绝写入）。
- Evidence 只记录查询事实，不记录 LLM 推理 / 不改变 VM/VLogs/MySQL SoT。

核心语义：
- 去重：相同 provenance_fingerprint → 复用同一 evidence_id，不重复计分（§三十五）。
- 生命周期：created → validated → expired → archived；旧证据 ≠ 当前事实（freshness isolation）。
- 不可变：metadata / provenance_fingerprint / raw_digest_sha256 不可变（frozen dataclass），仅 status 可迁移。
- 只记录不判断：本模块不做执行判断，不写 VM/VLogs/MySQL SoT。
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from datetime import datetime, timezone
from types import MappingProxyType
from typing import Any, Dict, List, Optional
from uuid import UUID

import contracts
from tool_result import ToolResult
from contracts_identity import (
    canonical_provenance_fields,
    evidence_id_from_fingerprint,
    provenance_fingerprint,
)

# §三十四 13 种 Evidence 类型
EVIDENCE_TYPES = {
    "metric_anomaly", "log_pattern", "log_error", "trace_anomaly", "k8s_state", "k8s_event",
    "alert", "change", "knowledge_case", "topology_relation", "resource_state",
    "capacity_anomaly", "hardware_event",
}

# §三十四 claim_type
CLAIM_TYPES = {"fact", "inference", "knowledge", "unknown"}

# claim_type=unknown 的 reason 枚举（冻结）
UNKNOWN_REASONS = {"insufficient_data", "permission_denied", "unavailable_source", "expired_evidence"}

# 事实来源（禁止 AI/LLM/Agent）
SOURCE_SYSTEMS = {"VM", "VLogs", "query-api", "MySQL", "k8sgpt", "knowledge"}

# 生命周期状态
EVIDENCE_STATUSES = {"created", "validated", "expired", "archived"}

_VALID_TRANSITIONS = {
    "created": {"validated", "expired", "archived"},
    "validated": {"expired", "archived"},
    "expired": {"archived"},
    "archived": set(),
}


def _sha256(*parts) -> str:
    h = hashlib.sha256()
    for p in parts:
        h.update(str(p).encode("utf-8"))
    return h.hexdigest()


@dataclass(frozen=True)
class Evidence:
    """只读证据对象（R2 收敛：组合权威 contracts.Evidence + 生命周期外部化）。

    不可变本体由权威 contracts.Evidence 承载（frozen 不可变）；
    生命周期字段 status/reliability/unknown_reason/supporting_evidence 在封装层管理
    （权威模型不含这些字段，避免污染 V1 wire）。
    字段访问兼容：权威字段经 __getattr__ 转发；UUID 字段返回小写 str（兼容既有调用方）。
    """

    __slots__ = ("contract", "status", "reliability", "unknown_reason", "supporting_evidence", "_frozen_meta")

    def __init__(
        self,
        contract: contracts.Evidence,
        *,
        status: str = "created",
        reliability: Optional[Dict[str, Any]] = None,
        unknown_reason: Optional[str] = None,
        supporting_evidence: Optional[List[str]] = None,
    ) -> None:
        if status not in EVIDENCE_STATUSES:
            raise ValueError(f"非法 status: {status}")
        if contract.source not in SOURCE_SYSTEMS:
            raise ValueError(f"非法 source（禁 AI/LLM/Agent）: {contract.source}")
        if contract.claim_type == "inference" and not (supporting_evidence or []):
            raise ValueError("claim_type=inference 必须引用 supporting_evidence")
        if contract.claim_type == "unknown":
            if unknown_reason is None:
                raise ValueError("claim_type=unknown 必须记录 unknown_reason")
            if unknown_reason not in UNKNOWN_REASONS:
                raise ValueError(f"非法 unknown_reason: {unknown_reason}")
        object.__setattr__(self, "contract", contract)
        object.__setattr__(self, "status", status)
        object.__setattr__(self, "reliability", dict(reliability or {}))
        object.__setattr__(self, "unknown_reason", unknown_reason)
        object.__setattr__(self, "supporting_evidence", list(supporting_evidence or []))

    # ── 权威字段转发 ────────────────────────────────────────────────────
    def __getattr__(self, name: str) -> Any:
        c = object.__getattribute__(self, "contract")
        if name in type(c).model_fields:
            val = getattr(c, name)
            if isinstance(val, UUID):
                return str(val)
            if isinstance(val, dict) and name == "metadata":
                # metadata 不可变（对齐原 frozen dataclass 语义）
                return MappingProxyType(dict(val))
            return val
        raise AttributeError(f"Evidence 无字段 {name!r}")

    @property
    def evidence_id(self) -> str:
        return str(self.contract.evidence_id)


class EvidenceHubError(ValueError):
    """Evidence Hub 违反隔离/归属不变量。"""


class EvidenceHub:
    """内存 Evidence Store（MVP）。真实 MySQL/MinIO 分层属后续阶段。"""

    def __init__(self) -> None:
        self._store: Dict[str, Evidence] = {}
        self._fingerprint_index: Dict[str, str] = {}

    def save(self, evidence: Evidence) -> Evidence:
        """保存 Evidence；相同 provenance_fingerprint 复用已有 evidence_id（去重，不重复计分）。"""
        fp = evidence.provenance_fingerprint
        if fp and fp in self._fingerprint_index:
            return self._store[self._fingerprint_index[fp]]
        if evidence.evidence_id in self._store:
            raise ValueError(f"evidence_id 重复: {evidence.evidence_id}")
        self._store[evidence.evidence_id] = evidence
        if fp:
            self._fingerprint_index[fp] = evidence.evidence_id
        return evidence

    def save_from_tool_result(
        self,
        tr: ToolResult,
        *,
        run_id: str,
        evidence_type: str,
        claim_type: str = "fact",
        supporting_evidence: Optional[List[str]] = None,
        unknown_reason: Optional[str] = None,
        resource_id: str = "",
        namespace: str = "",
        service: str = "",
        trace_id: str = "",
    ) -> Evidence:
        """从 P7.3 ToolResult 归一化创建 Evidence（LLM 推理绝不进入）。"""
        if evidence_type not in EVIDENCE_TYPES:
            raise ValueError(f"非法 evidence_type: {evidence_type}")
        if claim_type not in CLAIM_TYPES:
            raise ValueError(f"非法 claim_type: {claim_type}")
        if claim_type == "inference" and not supporting_evidence:
            raise ValueError("claim_type=inference 必须引用 supporting_evidence")
        if claim_type == "unknown":
            if unknown_reason is None:
                raise ValueError("claim_type=unknown 必须记录 unknown_reason")
            if unknown_reason not in UNKNOWN_REASONS:
                raise ValueError(f"非法 unknown_reason: {unknown_reason}")

        source = tr.source_system
        if source not in SOURCE_SYSTEMS:
            raise ValueError(f"非法 source（禁 AI/LLM/Agent）: {source}")

        raw_digest = _sha256(tr.summary, json.dumps(tr.data, sort_keys=True, default=str))
        t0 = (tr.time_range or "").split("/")[0] if tr.time_range else None
        t1 = (tr.time_range or "").split("/")[1] if tr.time_range and "/" in tr.time_range else None
        fp = _fingerprint(
            source=source,
            query_id=tr.query_id,
            resource_id=resource_id,
            time_range_start=t0,
            time_range_end=t1,
            digest=raw_digest,
            # 审计 P0-2：fingerprint 必须包含 tenant/cluster/run 隔离维度，
            # 否则相同 source/query/resource/time/digest 会让后续租户直接复用
            # 首个租户的 Evidence 对象（跨租户/跨 run 污染）。
            tenant_id=tr.tenant_id,
            cluster_id=tr.cluster_id,
            run_id=run_id,
        )
        # R2 方案 B：evidence_id = UUIDv5(FROZEN_NS, fingerprint)，实体身份为 UUID，fingerprint 仅去重键。
        if fp in self._fingerprint_index:
            existing = self._store[self._fingerprint_index[fp]]
            # 复用前逐项核验归属，杜绝跨租户/跨 cluster 复用
            if not _owned_by(existing, tr.tenant_id, tr.cluster_id, run_id):
                raise EvidenceHubError(
                    "Evidence fingerprint 冲突但归属不一致（tenant/cluster/run 隔离），拒绝复用"
                )
            return existing

        reliability = {
            "score": _source_reliability(source, evidence_type),
            "method": "source_reliability_table_v1",
            "calculated_at": datetime.now(timezone.utc).isoformat(),
        }
        contract = contracts.Evidence(
            evidence_id=evidence_id_from_fingerprint(fp),
            run_id=_as_uuid(run_id),
            tenant_id=_as_uuid(tr.tenant_id),
            cluster_id=_as_uuid(tr.cluster_id),
            evidence_type=evidence_type,
            claim_type=claim_type,
            source=source,
            source_reliability=reliability["score"],
            fact=tr.summary or (tr.error_message or ""),
            raw_ref=None,
            raw_digest_sha256=raw_digest,
            metadata=_build_metadata(tr, claim_type, unknown_reason, supporting_evidence),
            provenance_fingerprint=fp,
            resource_id=resource_id,
            namespace=namespace,
            service=service or getattr(tr, "service", ""),
            trace_id=trace_id,
            observed_at=tr.finished_at,
            time_range_start=_as_datetime(t0),
            time_range_end=_as_datetime(t1),
            created_at=datetime.now(timezone.utc),
        )
        ev = Evidence(
            contract,
            status="created",
            reliability=reliability,
            unknown_reason=unknown_reason,
            supporting_evidence=list(supporting_evidence or []),
        )
        self._store[ev.evidence_id] = ev
        self._fingerprint_index[fp] = ev.evidence_id
        return ev

    def get(self, evidence_id: str) -> Optional[Evidence]:
        return self._store.get(evidence_id)

    def transition(self, evidence_id: str, new_status: str) -> Evidence:
        """仅 status 可迁移（created→validated→expired→archived）。"""
        if new_status not in EVIDENCE_STATUSES:
            raise ValueError(f"非法 status: {new_status}")
        ev = self._store.get(evidence_id)
        if ev is None:
            raise KeyError(f"evidence 不存在: {evidence_id}")
        allowed = _VALID_TRANSITIONS.get(ev.status, set())
        if new_status not in allowed:
            raise ValueError(f"非法迁移: {ev.status} → {new_status}")
        updated = Evidence(
            ev.contract,
            status=new_status,
            reliability=ev.reliability,
            unknown_reason=ev.unknown_reason,
            supporting_evidence=ev.supporting_evidence,
        )
        self._store[evidence_id] = updated
        return updated

    def current_facts(self, tenant_id: str = "", cluster_id: str = "") -> List[Evidence]:
        """仅 validated 证据可作为当前事实依据（freshness isolation）。"""
        out = []
        for ev in self._store.values():
            if ev.status != "validated":
                continue
            if tenant_id and ev.tenant_id != tenant_id:
                continue
            if cluster_id and ev.cluster_id != cluster_id:
                continue
            out.append(ev)
        return out

    def reference_status(self, evidence_id: str) -> str:
        """Planner 引用证据时的新鲜度判定：validated=current，否则 stale。"""
        ev = self._store.get(evidence_id)
        if ev is None:
            return "missing"
        return "current" if ev.status == "validated" else "stale"

    def find_by_fingerprint(self, fp: str) -> Optional[Evidence]:
        eid = self._fingerprint_index.get(fp)
        return self._store.get(eid) if eid else None

    def all(self) -> List[Evidence]:
        return list(self._store.values())


def _source_reliability(source: str, evidence_type: str = "") -> float:
    """§三十六 Source Reliability V1 固定表（R2 合同收敛，审计点名"可靠性表"）。

    修复：
    - Raw Log 应为 0.85（原 VLogs=0.92 偏高）。
    - Knowledge 应区分 Runbook 0.65 / Historical Case 0.60（原统一 0.8）。
    - query-api=0.9 无法区分 Kubernetes state/Trace/Change → 按 evidence_type 细分。
    """
    # 按 evidence_type（§三十六）优先
    by_evidence_type = {
        "metric_anomaly": 0.95,     # Metric / SLI
        "k8s_state": 0.95,          # Kubernetes API current state
        "trace_anomaly": 0.90,      # Trace / Span
        "k8s_event": 0.90,          # Kubernetes Event
        "change": 0.90,             # Structured Change Record
        "topology_relation": 0.85,  # Resource Graph deterministic
        "resource_state": 0.85,
        "hardware_event": 0.85,     # DeepFlow observation
        "log_error": 0.85,          # Raw Log
        "log_pattern": 0.80,        # Log Pattern
        "capacity_anomaly": 0.80,
        "runbook": 0.65,            # Runbook / SOP
        "knowledge_case": 0.60,     # Historical Case
    }
    if evidence_type in by_evidence_type:
        return by_evidence_type[evidence_type]
    # fallback：source 映射（对齐 §三十六 语义）
    return {
        "VM": 0.95,
        "VLogs": 0.85,            # Raw Log → 0.85（修正 0.92）
        "query-api": 0.9,
        "MySQL": 0.98,
        "k8sgpt": 0.70,           # K8sGPT Diagnosis
        "runbook": 0.65,
        "knowledge": 0.60,        # Historical Case
    }.get(source, 0.5)


def _build_metadata(tr, claim_type: str, unknown_reason: Optional[str],
                    supporting_evidence: Optional[List[str]]) -> Dict[str, Any]:
    """构造权威 Evidence metadata（对齐权威 validator：unknown→reason，inference→supporting_evidence_ids）。"""
    meta: Dict[str, Any] = {"status": getattr(tr, "status", ""), "tool_id": getattr(tr, "tool_id", "")}
    if claim_type == "unknown" and unknown_reason:
        meta["reason"] = unknown_reason
    if claim_type == "inference" and supporting_evidence:
        meta["supporting_evidence_ids"] = list(supporting_evidence)
    return meta


def _as_uuid(value: Any) -> UUID:
    """把 run_id/tenant_id/cluster_id 规范为 UUID：已是 UUID 用小写，否则 UUIDv5 派生。

    权威 contracts.Evidence 要求 UUID 强类型；MVP 可能用非 UUID 标签，用固定 namespace 派生
    确定性 UUID（与 evidence_id 策略一致），保证可复现且不破坏隔离。
    """
    if isinstance(value, UUID):
        return value
    try:
        return UUID(str(value))
    except (ValueError, TypeError):
        import uuid as _uuid
        from contracts_identity import FROZEN_EVIDENCE_NAMESPACE
        return _uuid.uuid5(FROZEN_EVIDENCE_NAMESPACE, str(value))


def _as_datetime(value: Any) -> Optional[datetime]:
    """time_range 字符串（ISO，含时区）→ datetime（权威 contracts.Evidence 要求 datetime）。"""
    if value is None or str(value).strip() == "":
        return None
    try:
        return datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except ValueError:
        return None


def _fingerprint(
    source, query_id, resource_id, time_range_start, digest,
    time_range_end="", tenant_id="", cluster_id="", run_id="",
) -> str:
    """provenance fingerprint（R2 方案 B 固定，见 contracts_identity.canonical_provenance_fields）。

    审计 P0-2：必须含 tenant/cluster/run 隔离维度；含 time_range_end（对齐跨语言黄金向量）。
    相同 source/query/resource/time/digest 的 Evidence，若属于不同 tenant/cluster/run，
    必须是不同 fingerprint（防跨租户/跨 run 复用同一 Evidence 对象）。
    """
    return provenance_fingerprint(
        canonical_provenance_fields(
            source=source,
            query_id=query_id,
            resource_id=resource_id,
            time_range_start=time_range_start,
            time_range_end=time_range_end,
            digest=digest,
            tenant_id=tenant_id,
            cluster_id=cluster_id,
            run_id=run_id,
        )
    )


def _owned_by(ev, tenant_id, cluster_id, run_id) -> bool:
    """复用前核验 Evidence 归属（防跨 tenant/cluster/run 复用）。"""
    return (
        ev.tenant_id == tenant_id
        and ev.cluster_id == cluster_id
        and ev.run_id == run_id
    )
