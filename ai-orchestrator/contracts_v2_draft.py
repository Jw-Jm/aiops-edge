"""R2 V2 草案 Schema（STATUS=DRAFT，NOT authoritative，NOT part of V1 wire）。

背景（2026-08-21 评审阻断项 1）：
- V1 冻结 `contracts.ToolResult` 是 15 字段（对齐 Python/TS binding + 共享 fixture；Go 内部 Error 不入 wire），
  包含 `tenant_id/tool_id/request_id/retry_policy/evidence_required/duration_ms/
  provenance/partial_reason/denied_scope` 会改变 Python Schema 与默认序列化形态，
  且这些字段不在 Go/TS `ToolResult` 中 → 违反"V1 wire contract 冻结"。
- 本文件把这些平行 `tool_result.py` 演进字段隔离为独立 V2 草案，不混入 V1 类。
- V2 草案只有在完成"三端对齐 + Conformance Vectors + 独立 wire_version"后，才可能升级为权威。
  在此之前，任何生产/测试不得把它当 V1 权威序列化；调用方收敛仍基于平行 tool_result.py。

V2 草案的字段仅为平行 `tool_result.py.ToolResult` 的演进映射，供 R2 Task-1 后续设计参考：
- tenant_id          : 强隔离维度（P0-2）
- tool_id            : Tool 标识
- request_id         : 请求追溯
- retry_policy       : 重试策略
- evidence_required  : Evidence 强制要求
- duration_ms        : 执行耗时
- provenance         : 来源追溯
- partial_reason     : partial 原因（timeout_partial/source_partial/permission_partial）
- denied_scope       : 权限拒绝范围

注意：Evidence 引用（evidence_ids）保持 V1 冻结的 UUID 引用，V2 草案不得放宽。
"""
from __future__ import annotations

from typing import Any, Dict

# 保留平行字段的枚举/常量，防止 V2 草案与 tool_result.py 语义漂移（仅设计引用，不做独立校验）。
DRAFT_STATUSES = {
    "success", "partial", "no_data", "failed", "timeout", "unavailable", "permission_denied",
}
DRAFT_PARTIAL_REASONS = {"timeout_partial", "source_partial", "permission_partial"}


# 以下仅作为 V2 草案字段清单的文档化锚点（不实例化、不参与 V1 wire）。
# 设计期保留：V2 草案字段需在"三端对齐 + 独立 wire_version"后才可能升级。
# 收敛实现（ACL / contracts_v2）在独立授权后进行。
class ToolResultV2DraftFields:
    """V2 草案字段清单（相对 V1 冻结 ToolResult 的增量）。仅设计锚点，勿序列化。"""

    __slots__ = ()
    FIELDS: Dict[str, Any] = {
        "tenant_id": None,
        "tool_id": None,
        "request_id": None,
        "retry_policy": None,
        "evidence_required": False,
        "duration_ms": None,
        "provenance": None,
        "partial_reason": None,
        "denied_scope": None,
    }
