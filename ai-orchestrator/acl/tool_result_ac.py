"""R2 Task 1 — ToolResult ACL（权威 contracts.py ↔ 平行 tool_result.py 互转）。

职责：类型适配（cluster_id str→UUID、time_range str→Dict、status str→ToolStatus）+ 校验。
R2 方案 B（评审阻断项 2/3/4）修正：
- 只适配 V1 冻结 15 字段（对齐 Python/TS binding），不含 V2 草案字段。
- evidence_ids 引用规则：只接受 UUID；非 UUID legacy 字符串通过 fingerprint_index
  解析并验证实体存在，否则 fail-closed（禁止为任意字符串无条件派生 UUID 制造悬空引用）。

Task 1 完成原子切换后立即删除本适配器。
"""
from __future__ import annotations

from typing import Any, Dict, Optional
from uuid import UUID

from contracts import ToolResult as ContractToolResult
from contracts_identity import resolve_evidence_id


class ToolResultAcError(ValueError):
    def __init__(self, message: str):
        self.error_code = "TOOL_RESULT_AC_ERROR"
        super().__init__(message)


def to_contract(
    tr: Any,
    *,
    fingerprint_index: Optional[Dict[str, str]] = None,
    existing_ids: Optional[Any] = None,
) -> ContractToolResult:
    """平行 tool_result.py ToolResult → 权威 V1 contracts.ToolResult（类型适配）。

    fingerprint_index: provenance_fingerprint(str) → evidence_id(UUID 字符串) 的映射，
    用于把非 UUID 的 legacy 证据引用解析到已存在实体（阻断项 4）。
    existing_ids: 已存在 Evidence 实体的 evidence_id(UUID) 可迭代集合；用于验证
    UUID 引用确实对应实体，未知/悬空引用 fail-closed（Bugbot B3）。
    """
    try:
        cluster_uuid = UUID(str(tr.cluster_id))
    except (ValueError, TypeError) as e:
        raise ToolResultAcError(f"cluster_id 非法（需 canonical UUID）: {tr.cluster_id!r}") from e

    evidence_ids = []
    for e in (tr.evidence_ids or []):
        try:
            evidence_ids.append(
                resolve_evidence_id(e, fingerprint_index=fingerprint_index,
                                    existing_ids=existing_ids)
            )
        except ValueError as ex:
            raise ToolResultAcError(str(ex)) from ex

    return ContractToolResult(
        tool_name=tr.tool_name,
        cluster_id=cluster_uuid,
        status=tr.status,
        success=tr.status in ("success", "partial", "no_data"),
        summary=tr.summary,
        data=tr.data,
        error_code=tr.error_code,
        error_message=tr.error_message,
        retryable=tr.retryable,
        evidence_ids=evidence_ids,
        source_system=tr.source_system,
        query_id=tr.query_id,
        time_range=_parse_time_range(tr.time_range),
        started_at=tr.started_at,
        finished_at=tr.finished_at,
    )


def _parse_time_range(tr: Any) -> Optional[Dict[str, Any]]:
    """time_range "start/end" → {"start": ..., "end": ...}。"""
    if not tr:
        return None
    if isinstance(tr, dict):
        return tr
    parts = str(tr).split("/")
    if len(parts) == 2:
        return {"start": parts[0], "end": parts[1]}
    return {"value": str(tr)}
