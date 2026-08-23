"""生产 RCA 适配器 — 评审加固测试。

评审修复（P0）：生产 /api/v1/ops/rca* 与 node_rca 必须收敛到新 Run/Evidence/RCA 链。
rca_production 提供与 full_rca_analysis 兼容的返回结构，内部走 RcaEngine（Evidence-driven），
不再是旧确定性/假设引擎独立判根因。

验证：
- 返回结构兼容（含 mode/result/root cause/confidence）。
- 从 Evidence 走 RcaEngine 产出根因，禁止绕过 Evidence。
- 无 Evidence / 无法判定 → 兼容的 unknown 结构。
"""
import uuid
from datetime import datetime, timezone

import contracts as C

RUN = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
TENANT = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
CLUSTER = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"


def _eid(label):
    from contracts_identity import FROZEN_PLAN_STEP_NS
    return uuid.uuid5(FROZEN_PLAN_STEP_NS, f"ev:{label}")


def _evidence(eid, etype, source, reliability, fact):
    from evidence_hub import Evidence

    return Evidence(
        C.Evidence(
            evidence_id=_eid(eid),
            run_id=RUN, tenant_id=TENANT, cluster_id=CLUSTER,
            evidence_type=etype, claim_type="fact", source=source,
            source_reliability=reliability, fact=fact,
            raw_digest_sha256=f"digest-{eid}",
            provenance_fingerprint=f"fp-{eid}",
            created_at=datetime.now(timezone.utc),
        )
    )


def test_production_adapter_returns_compatible_structure():
    from rca_production import run_rca_production

    evidences = [
        _evidence("ev-m", "metric_anomaly", "VM", 0.95, "error rate spike"),
        _evidence("ev-l", "log_error", "VLogs", 0.85, "exception"),
        _evidence("ev-t", "trace_anomaly", "query-api", 0.90, "timeout"),
    ]
    result = run_rca_production(
        service="checkout", cluster_id=CLUSTER, evidences=evidences,
        run_id=RUN, tenant_id=TENANT, llm_prior=0.8,
    )
    # 兼容结构：含 mode + result（root cause / confidence）
    assert "mode" in result
    assert "result" in result
    assert result["mode"] == "evidence_rca"
    r = result["result"]
    assert "confidence" in r


def test_production_adapter_uses_evidence_not_prompt():
    from rca_production import run_rca_production

    # 无 Evidence → 不能凭空判根因（RCA without Evidence 风险解除）
    result = run_rca_production(
        service="checkout", cluster_id=CLUSTER, evidences=[],
        run_id=RUN, tenant_id=TENANT,
    )
    assert result["mode"] == "evidence_rca"
    r = result["result"]
    # 权威 root_cause=None → rca_production 输出 ""（方案 B）
    assert r.get("root_cause") == ""
    assert r.get("confidence", 0) <= 0.6  # 无法达 confirmed


def test_production_adapter_keeps_unknown_safe():
    from rca_production import run_rca_production

    result = run_rca_production(
        service="checkout", cluster_id=CLUSTER, evidences=[],
        run_id=RUN, tenant_id=TENANT,
    )
    assert result["result"].get("automatic_remediation") is False
