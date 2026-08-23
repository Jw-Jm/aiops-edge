"""RCA 生产适配器 — V9.3 Phase 9（评审加固版）。

评审修复（P0）：生产 /api/v1/ops/rca* 与 node_rca 必须收敛到新 Run/Evidence/RCA 链。
本模块提供与既有 `rca.full_rca_analysis` 兼容的返回结构，内部走 RcaEngine
（Evidence-driven Phase 9 链），禁止绕过 Evidence 独立判根因。

设计：
- 输入：Evidence 列表（来自 Evidence Hub）+ run/tenant/cluster/service 上下文。
- 内部：RcaEngine.run → RcaResult。
- 输出：兼容 dict { mode: "evidence_rca", result: { root_cause, confidence, ... } }，
  使前端 / node_rca 无需改动即可消费。

边界：
- 只接受 Evidence（LLM inference 不进入，F3）。
- 无 Evidence / 无法判定 → unknown-safe（root_cause=unknown / no auto remediation）。
"""
from __future__ import annotations

from typing import Any, List

from rca_engine import RcaEngine


def run_rca_production(
    *,
    service: str,
    cluster_id: str,
    evidences: List[Any],
    run_id: str,
    tenant_id: str,
    intent_id: str = "prod-rca",
    llm_prior: float = 0.0,
) -> dict:
    """生产 RCA 入口：从 Evidence 走 RcaEngine，返回兼容结构。"""
    resource_id = f"{cluster_id}/svc/{service}" if service else cluster_id
    symptoms = [service] if service else ["resource anomaly"]

    engine = RcaEngine(tenant_id=tenant_id, cluster_id=cluster_id)
    result = engine.run(
        run_id=run_id,
        intent_id=intent_id,
        resource_id=resource_id,
        symptoms=symptoms,
        evidences=evidences,
        llm_reasoning_prior=llm_prior,
    )

    # 兼容 full_rca_analysis 返回结构（R2 收敛 v0.3 §9.1）
    # result 现在是权威 contracts.RcaResult：root_cause 用 None 表达 unknown；confidence 直接用权威值。
    root_cause = result.root_cause or ""
    missing_types = [m.required_type or m.description for m in result.missing_evidence]
    return {
        "mode": "evidence_rca",
        "result": {
            "root_cause_service": root_cause,
            "root_cause": root_cause,
            "confidence": result.confidence,  # 直接用权威 confidence（不再枚举映射）
            "confidence_state": result.conclusion_state,  # 权威 conclusion_state
            "evidence_chain": [
                {"type": m, "role": "missing"} for m in missing_types
            ],
            "missing_evidence": missing_types,
            "automatic_remediation": result.automatic_remediation,
        },
    }
