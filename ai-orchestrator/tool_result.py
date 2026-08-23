"""P7.3 ToolResult Normalization — V9.3 Phase7 统一 Tool 返回结构与精确语义归一化。

核心原则（§94 红线，F3）：
- 精确区分 7 态 status（success/partial/no_data/failed/timeout/unavailable/permission_denied）。
- 禁止降级：permission_denied→no_data、no_data→healthy、unavailable→healthy、403→no_data、
  network error→no_data 全部禁止（降级会把权限问题伪装成无数据 / 把不可用伪装成健康）。
- retryable：仅 failed/timeout/unavailable 可重试（受 tool.retry 上限）；permission_denied/no_data 不可重试。
- source_system 禁止 AI/LLM/Agent（事实只能来自真实数据源）。
- ToolResult 只记录查询事实，不包含 LLM inference（LLM 推理绝不作为 Evidence，见 P7.4）。
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

from tool_registry import ToolDefinition

# §28.4 / P7.3 7 态
STATUSES = {
    "success", "partial", "no_data", "failed", "timeout", "unavailable", "permission_denied",
}

# partial 原因（评审建议 2）：避免 partial 成为黑箱
PARTIAL_REASONS = {"timeout_partial", "source_partial", "permission_partial"}

# 事实来源（禁止 AI/LLM/Agent）
SOURCE_SYSTEMS = {"VM", "VLogs", "query-api", "MySQL", "k8sgpt", "knowledge"}

# 可重试的状态集合（§94：permission_denied/no_data 不可重试）
RETRYABLE_STATUSES = {"failed", "timeout", "unavailable"}


@dataclass
class ToolExecutionRecord:
    """内部业务载体（R2-A1 承载边界：非跨语言合同）。

    V1 权威 wire 是 contracts.ToolResult（15 字段冻结）；本类是内部业务载体，
    承载 V1 15 字段 + 9 个 V2 审计字段（tenant_id/tool_id/request_id/retry_policy/
    evidence_required/duration_ms/provenance/partial_reason/denied_scope）。
    V2 字段不上 wire（如需上走独立 ToolResultV2 草案，不暗改 V1）。
    """

    tool_name: str
    tool_id: str
    cluster_id: str
    tenant_id: str
    status: str
    summary: str
    data: Dict[str, Any]
    error_code: str
    error_message: str
    retryable: bool
    retry_policy: Dict[str, Any]
    evidence_ids: List[str]
    evidence_required: bool
    source_system: str
    request_id: str
    query_id: str
    time_range: str
    started_at: datetime
    finished_at: datetime
    duration_ms: int
    provenance: Dict[str, Any]
    partial_reason: Optional[str] = None
    denied_scope: Optional[Dict[str, Any]] = None

    def __post_init__(self) -> None:
        if self.status not in STATUSES:
            raise ValueError(f"非法 status: {self.status}")
        if self.source_system and self.source_system not in SOURCE_SYSTEMS:
            raise ValueError(f"非法 source_system: {self.source_system}")
        if self.partial_reason is not None and self.partial_reason not in PARTIAL_REASONS:
            raise ValueError(f"非法 partial_reason: {self.partial_reason}")

    def to_dict(self) -> Dict[str, Any]:
        return {
            "tool_name": self.tool_name, "tool_id": self.tool_id,
            "cluster_id": self.cluster_id, "tenant_id": self.tenant_id,
            "status": self.status, "summary": self.summary, "data": self.data,
            "error_code": self.error_code, "error_message": self.error_message,
            "retryable": self.retryable, "retry_policy": self.retry_policy,
            "evidence_ids": self.evidence_ids, "evidence_required": self.evidence_required,
            "source_system": self.source_system, "request_id": self.request_id,
            "query_id": self.query_id, "time_range": self.time_range,
            "started_at": self.started_at.isoformat(), "finished_at": self.finished_at.isoformat(),
            "duration_ms": self.duration_ms, "provenance": self.provenance,
            "partial_reason": self.partial_reason, "denied_scope": self.denied_scope,
        }


def validate_tool_result(tr: ToolExecutionRecord) -> Optional[str]:
    """校验 ToolExecutionRecord 完整性；合法返回 None，否则返回错误消息。"""
    if tr.status not in STATUSES:
        return f"非法 status: {tr.status}"
    if not tr.source_system:
        return "source_system 必填"
    if tr.source_system not in SOURCE_SYSTEMS:
        return f"source_system 非法: {tr.source_system}"
    if not tr.request_id:
        return "request_id 必填"
    if not tr.query_id:
        return "query_id 必填"
    if not tr.time_range:
        return "time_range 必填"
    return None


def _normalize_source(source_system: str) -> str:
    """审计 P0-3：非法来源必须拒绝，禁止改写成 query-api 伪装。

    未知/非法 source_system（如 LLM/Agent/任意字符串）若被 fail-closed 到 query-api，
    会把"非法来源的事实"伪装成"query-api 事实"，破坏 Evidence 来源边界与可靠性评分。
    正确行为：直接拒绝（fail-closed），由调用方保证只传真实数据源。
    """
    if source_system in SOURCE_SYSTEMS:
        return source_system
    raise ValueError(
        f"非法 source_system（禁 AI/LLM/Agent/未知来源伪装）: {source_system!r}"
    )


def _detect(outcome) -> tuple:
    """从 outcome 提取 (status:int, body:dict, kind:Optional[str])。

    支持 QueryResult / InternalQueryError（P7.2）以及通用 (status, body) 对象。
    """
    status = getattr(outcome, "http_status", getattr(outcome, "status", None))
    body = getattr(outcome, "body", None) or {}
    kind = getattr(outcome, "kind", None)
    if not isinstance(body, dict):
        body = {}
    return status, body, kind


def _map_kind(kind: str) -> str:
    """InternalQueryError kind → ToolResult status。禁止降级。"""
    if kind == "permission_denied":
        return "permission_denied"
    if kind == "unavailable":
        return "unavailable"
    if kind == "timeout":
        return "timeout"
    # service_auth_failed / scope_mismatch / validation_failed / not_found / internal → failed
    return "failed"


def _map_http(status: int, body: dict) -> str:
    """HTTP 状态 → ToolResult status。禁止降级。"""
    if status == 200:
        if body.get("error") == "NO_DATA" or _is_empty_payload(body):
            return "no_data"
        return "success"
    if status == 403:
        return "permission_denied"
    if status == 503:
        return "unavailable"
    if status == 504:
        return "timeout"
    return "failed"


def _is_empty_payload(body: dict) -> bool:
    """200 响应是否为空结果（无实际数据）。空 dict 或所有字段均为空值 → no_data。"""
    if not body:
        return True
    for value in body.values():
        if isinstance(value, (list, dict)):
            if value:
                return False
        elif value not in (None, "", 0, False):
            return False
    return True


def normalize_tool_result(
    *,
    outcome: Any,
    tool: ToolDefinition,
    tenant_id: str,
    cluster_id: str,
    request_id: str,
    query_id: str,
    time_range: str,
    source_system: str,
    started_at: datetime,
    finished_at: datetime,
    retry_policy: Optional[Dict[str, Any]] = None,
    evidence_ids: Optional[List[str]] = None,
    evidence_required: Optional[bool] = None,
) -> ToolResult:
    """把一次查询执行结果精确归一化为标准 ToolResult（7 态，禁止降级）。"""
    status, body, kind = _detect(outcome)
    if kind:
        result_status = _map_kind(kind)
    else:
        result_status = _map_http(status, body)

    error_code = body.get("error", "") if isinstance(body, dict) else ""
    error_message = body.get("message", "") if isinstance(body, dict) else ""
    if kind and not error_code:
        error_code = kind.upper()

    retryable = result_status in RETRYABLE_STATUSES
    policy = dict(retry_policy) if retry_policy else {"max_attempts": tool.retry, "backoff": 1.0}

    denied_scope = None
    if result_status == "permission_denied":
        denied_scope = {
            "required_capability": tool.required_capability,
            "denied_scope": f"tenant:{tenant_id}/cluster:{cluster_id}",
        }

    duration_ms = int(
        (finished_at.astimezone(timezone.utc) - started_at.astimezone(timezone.utc)).total_seconds() * 1000
    ) if finished_at and started_at else 0

    return ToolResult(
        tool_name=tool.name,
        tool_id=tool.tool_id,
        cluster_id=cluster_id,
        tenant_id=tenant_id,
        status=result_status,
        summary=_summary(result_status, tool.name),
        data=body,
        error_code=error_code,
        error_message=error_message,
        retryable=retryable,
        retry_policy=policy,
        evidence_ids=list(evidence_ids or []),
        evidence_required=evidence_required if evidence_required is not None else tool.evidence_required,
        source_system=_normalize_source(source_system),
        request_id=request_id,
        query_id=query_id,
        time_range=time_range,
        started_at=started_at,
        finished_at=finished_at,
        duration_ms=duration_ms,
        provenance={
            "tool_id": tool.tool_id,
            "request_id": request_id,
            "trusted_context_id": query_id,
            "source_timestamp": finished_at.isoformat() if finished_at else "",
        },
        denied_scope=denied_scope,
    )


def combine_partial(
    results: List[Any],
    partial_reason: str,
    *,
    tool: ToolDefinition,
    tenant_id: str,
    cluster_id: str,
    request_id: str,
    query_id: str,
    time_range: str,
    source_system: str,
    started_at: datetime,
    finished_at: datetime,
    retry_policy: Optional[Dict[str, Any]] = None,
) -> ToolResult:
    """多 backend 结果合并为 partial（部分成功 + 部分失败）。partial_reason 必填。"""
    if partial_reason not in PARTIAL_REASONS:
        raise ValueError(f"非法 partial_reason: {partial_reason}")
    merged: Dict[str, Any] = {}
    for r in results:
        _, body, _ = _detect(r)
        if isinstance(body, dict):
            merged.update(body)
    base = normalize_tool_result(
        outcome=(200, merged),
        tool=tool,
        tenant_id=tenant_id,
        cluster_id=cluster_id,
        request_id=request_id,
        query_id=query_id,
        time_range=time_range,
        source_system=source_system,
        started_at=started_at,
        finished_at=finished_at,
        retry_policy=retry_policy,
    )
    return ToolResult(
        tool_name=base.tool_name, tool_id=base.tool_id, cluster_id=base.cluster_id,
        tenant_id=base.tenant_id, status="partial", summary=_summary("partial", tool.name),
        data=base.data, error_code=base.error_code, error_message=base.error_message,
        retryable=False, retry_policy=base.retry_policy, evidence_ids=base.evidence_ids,
        evidence_required=base.evidence_required, source_system=base.source_system,
        request_id=base.request_id, query_id=base.query_id, time_range=base.time_range,
        started_at=base.started_at, finished_at=base.finished_at, duration_ms=base.duration_ms,
        provenance=base.provenance, partial_reason=partial_reason,
    )


def _summary(status: str, tool_name: str) -> str:
    if status == "success":
        return f"{tool_name} 查询成功"
    if status == "no_data":
        return f"{tool_name} 无数据"
    if status == "partial":
        return f"{tool_name} 部分数据"
    if status == "permission_denied":
        return f"{tool_name} 权限不足"
    if status == "timeout":
        return f"{tool_name} 超时"
    if status == "unavailable":
        return f"{tool_name} 数据源不可用"
    return f"{tool_name} 查询失败"


# ── R2-A1 承载边界：兼容别名（过渡期，消费方全切到 ToolExecutionRecord 后移除）──
# ToolResult 曾是平行 dataclass 名；现内部载体改名 ToolExecutionRecord，别名兼容既有 import。
ToolResult = ToolExecutionRecord
