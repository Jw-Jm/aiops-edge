"""P7.10 Security Gate — V9.3 Phase7 横切所有 Phase7 域的安全断言（Negative Tests）。

核心：Agent 不能扩展权限、不能绕过 approval、不能越 cluster。
本模块聚合 P7.1-P7.9 组件，提供一组 fail-closed 的 deny_* 检查（违规 raise SecurityViolation）。

Negative Tests（P7.10 必列）：
- unregistered Tool
- LLM 修改 risk / read_only
- wrong capability
- cross-cluster Tool
- budget exceeded
- ambiguous target
- backend unavailable（语义保留）
- permission denied（不降级）
- unknown/unregistered canonical cluster
- registered mapping 指向错误 canonical cluster
"""
from __future__ import annotations

import hashlib
import json
from typing import Dict, Optional

from internal_query_client import OPERATION_ROUTES

_RANK = {"R0": 0, "R1": 1, "R2": 2, "R3": 3, "R4": 4}


class SecurityViolation(Exception):
    def __init__(self, message: str, code: str = "SECURITY_VIOLATION"):
        self.error_code = code
        super().__init__(message)


class SecurityGate:
    """横切安全断言器（内存 MVP）。每项违规 raise SecurityViolation。"""

    def __init__(
        self,
        *,
        registry=None,
        planner=None,
        intent_engine=None,
        data_source_mapping=None,
        evidence_hub=None,
        state_store=None,
        manual_boundary=None,
    ) -> None:
        from data_source_mapping import DataSourceMapping
        from evidence_hub import EvidenceHub
        from intent_engine import IntentEngine
        from investigation_state import StateStore
        from manual_boundary import ManualBoundary
        from planner import Planner
        from tool_registry import ToolRegistry

        self.registry = registry or ToolRegistry
        self.planner = planner or Planner()
        self.intent_engine = intent_engine or IntentEngine()
        self.data_source_mapping = data_source_mapping or DataSourceMapping()
        self.evidence_hub = evidence_hub or EvidenceHub()
        self.state_store = state_store or StateStore()
        self.manual_boundary = manual_boundary or ManualBoundary()

    # ── T1 越权拒绝 ─────────────────────────────────────

    def deny_unregistered_tool(self, tool_id: str) -> None:
        if self.registry.get(tool_id) is None:
            raise SecurityViolation(f"unregistered Tool 被拒绝: {tool_id}", "UNREGISTERED_TOOL")

    def deny_risk_downgrade(self, tool_id: str, new_risk: str) -> None:
        t = self.registry.get(tool_id)
        if t is None:
            raise SecurityViolation(f"unregistered Tool: {tool_id}", "UNREGISTERED_TOOL")
        if _RANK.get(new_risk, 0) < _RANK.get(t.risk_level, 0):
            raise SecurityViolation(f"LLM 不能降级 Tool risk: {tool_id}", "RISK_DOWNGRADE_DENIED")

    def deny_readonly_change(self, tool_id: str) -> None:
        # 安全红线：任何尝试修改 read_only 均拒绝（Agent 不能改变执行边界）
        if self.registry.get(tool_id) is None:
            raise SecurityViolation(f"unregistered Tool: {tool_id}", "UNREGISTERED_TOOL")
        raise SecurityViolation(f"LLM 不能修改 Tool read_only: {tool_id}", "READONLY_IMMUTABLE")

    def deny_wrong_capability(self, tool_id: str, operation: str) -> None:
        t = self.registry.get(tool_id)
        if t is None:
            raise SecurityViolation(f"unregistered Tool: {tool_id}", "UNREGISTERED_TOOL")
        route = OPERATION_ROUTES.get(operation)
        if route is None:
            raise SecurityViolation(f"未知 operation: {operation}", "INVALID_CONTEXT")
        required_capability = route[1]
        if t.capability != required_capability:
            raise SecurityViolation(
                f"wrong capability: {tool_id}({t.capability}) 调用 {operation}({required_capability})",
                "CAPABILITY_MISMATCH",
            )

    # ── T2 Cross-Cluster / Scope ─────────────────────────

    def deny_cross_cluster(self, request_cluster: str, run_cluster: str) -> None:
        if request_cluster != run_cluster:
            raise SecurityViolation(
                f"cross-cluster Tool 调用被拒绝: {request_cluster} != {run_cluster}", "CROSS_CLUSTER_DENIED"
            )

    def deny_unknown_cluster(self, cluster_id: str) -> None:
        try:
            self.data_source_mapping.resolve_cluster(cluster_id)
        except Exception:
            raise SecurityViolation(f"未知/未注册 canonical cluster: {cluster_id}", "CLUSTER_FAIL_CLOSED") from None

    def deny_wrong_mapping(self, cluster_id: str) -> None:
        # 注册映射指向错误 canonical cluster → 拒绝
        if cluster_id not in self.data_source_mapping._registered_clusters:
            raise SecurityViolation(f"注册映射指向错误 canonical cluster: {cluster_id}", "CLUSTER_FAIL_CLOSED")

    # ── T3 Budget / 语义 ─────────────────────────────────

    def deny_budget_exceeded(self, plan) -> None:
        if getattr(plan, "status", None) == "budget_exceeded":
            raise SecurityViolation("budget exceeded 终止（BUDGET_EXCEEDED）", "BUDGET_EXCEEDED")

    def deny_ambiguous_target(self, *, target_resource_id: Optional[str], target_type: str) -> None:
        if target_type != "cluster" and not target_resource_id:
            raise SecurityViolation("ambiguous target（RESOURCE_AMBIGUOUS），禁止猜", "RESOURCE_AMBIGUOUS")

    # ── Registry Snapshot Hash（防运行期 Tool 偷换）────────

    def snapshot_registry(self, *, version: str = "v1") -> Dict:
        """生成 ToolRegistrySnapshot：防运行期 Tool 被替换 + Evidence/Plan 可引用 Registry 版本。"""
        tools = sorted(
            (t.tool_id, t.capability, t.risk_level, t.read_only, t.lifecycle_status)
            for t in self.registry.list_all()
        )
        digest = _snapshot_hash(tools)
        return {"version": version, "tools": tools, "hash": digest}

    def assert_registry_integrity(self, snapshot: Dict) -> None:
        """验证当前 Registry 与快照一致（未被运行期偷换）。"""
        current_tools = sorted(
            (t.tool_id, t.capability, t.risk_level, t.read_only, t.lifecycle_status)
            for t in self.registry.list_all()
        )
        if _snapshot_hash(current_tools) != snapshot.get("hash"):
            raise SecurityViolation("运行期 Tool Registry 被替换（快照 hash 不匹配）", "REGISTRY_TAMPERED")


def _snapshot_hash(tools) -> str:
    return hashlib.sha256(json.dumps(tools, sort_keys=True).encode("utf-8")).hexdigest()
