"""R2 Task 0 — Contract Bundle 骨架测试（2026-08-21 Bugbot B6 增强）。

验证：
- bundle manifest/invariants/conformance-vectors/schema 就位；
- 三端工具链固定、output 覆盖既有 binding（不新建第二合同层）；
- 黄金向量的 fingerprint 与 UUIDv5 关系真实可复算；
- 正负向量符合权威合同（UUID evidence_id、V1 冻结字段）。
"""
import hashlib
import json
import uuid
from pathlib import Path


def _bundle_dir():
    return Path(__file__).resolve().parents[2] / "docs" / "contracts" / "bundle" / "v2"


def test_bundle_manifest_exists_and_locks_tools():
    m = json.loads((_bundle_dir() / "manifest.json").read_text())
    assert m["bundle_version"] == "v2.0"
    assert m["wire_version"] == "v1"
    assert m["tools"]["python"]["name"] == "datamodel-codegen"
    assert m["tools"]["go"]["name"] == "go-generate"
    assert m["tools"]["ts"]["name"] == "quicktype"
    # output 必须覆盖既有 binding，禁止新建第二合同层
    assert m["tools"]["python"]["output"].endswith("contracts.py")
    assert m["tools"]["ts"]["output"].endswith("contracts.ts")
    assert m["tools"]["go"]["output"].endswith("context.go")
    # v1_frozen 是 dict：fields 精确 15，forbidden 不含收敛 4 模型字段
    v1 = m["v1_frozen"]
    assert v1["tool_result_fields"] == 15
    assert len(v1["fields"]) == 15
    assert "tenant_id" in v1["forbidden"]  # V2 草案字段禁入 V1
    assert "TrustedRequestContext" in v1["context_unsigned"]
    # checksum 已计算，非 PENDING
    assert m["checksum"] != "PENDING-CONVERGENCE"
    assert len(m["checksum"]) == 64
    # schema 至少 ToolResult 已就位
    assert m["schema"]["tool_result"].endswith("tool-result.schema.json")


def test_bundle_invariants_cover_reliability_and_fingerprint():
    text = (_bundle_dir() / "invariants.md").read_text()
    assert "metric_anomaly=0.95" in text
    assert "provenance_fingerprint" in text
    assert "confirmed" in text  # Hypothesis confirmed 条件
    assert "run_id" in text  # fingerprint 含 run


def test_bundle_conformance_vectors_include_negative():
    v = json.loads((_bundle_dir() / "conformance-vectors.json").read_text())
    assert "positive" in v["vectors"]
    assert "negative" in v["vectors"]
    assert "evidence_llm_source" in v["vectors"]["negative"]
    assert "unknown_schema_version" in v["vectors"]["negative"]
    # B6：新增负向量（非 UUID evidence_ids / 非 UUID evidence_id / fingerprint 不匹配）
    assert "tool_result_non_uuid_evidence_ids" in v["vectors"]["negative"]
    assert "evidence_non_uuid_id" in v["vectors"]["negative"]
    assert "evidence_fingerprint_mismatch" in v["vectors"]["negative"]


def test_bundle_positive_vectors_uuid_and_frozen_fields():
    """正向量必须符合权威合同：evidence_id 为 UUID；tool_result 为 V1 冻结 15 字段。"""
    v = json.loads((_bundle_dir() / "conformance-vectors.json").read_text())
    ev = v["vectors"]["positive"]["evidence_valid"]
    uuid.UUID(ev["evidence_id"])  # 必须可解析为 UUID
    uuid.UUID(ev["run_id"])
    uuid.UUID(ev["tenant_id"])
    uuid.UUID(ev["cluster_id"])
    tr = v["vectors"]["positive"]["tool_result_success"]
    v1_fields = {"tool_name", "cluster_id", "success", "status", "summary", "data",
                 "error_code", "error_message", "retryable", "evidence_ids",
                 "source_system", "query_id", "time_range", "started_at", "finished_at"}
    assert set(tr.keys()) == v1_fields, "tool_result 正向样例字段数不匹配 V1 冻结 15 字段"
    # evidence_ids 引用必须是 UUID
    for eid in tr["evidence_ids"]:
        uuid.UUID(eid)


def test_bundle_positive_vectors_pass_authoritative_models():
    """正向量必须能被权威 contracts 模型 model_validate 接受（Bugbot C6）。"""
    import sys
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    import contracts

    v = json.loads((_bundle_dir() / "conformance-vectors.json").read_text())

    # tool_result_success → contracts.ToolResult（15 字段）
    tr = contracts.ToolResult.model_validate(v["vectors"]["positive"]["tool_result_success"])
    assert tr.success is True and tr.status == "success"

    # evidence_valid → contracts.Evidence（不得含权威不接受的额外字段）
    ev = contracts.Evidence.model_validate(v["vectors"]["positive"]["evidence_valid"])
    assert ev.evidence_id is not None
    assert ev.provenance_fingerprint


def test_bundle_checksum_recomputable():
    """按 manifest 规则复算 checksum 必须等于 manifest.checksum（Bugbot C6）。"""
    m = json.loads((_bundle_dir() / "manifest.json").read_text())
    vectors = (_bundle_dir() / "conformance-vectors.json").read_bytes()
    invariants = (_bundle_dir() / "invariants.md").read_bytes()
    schema_dir = _bundle_dir() / "schema"
    schema_bytes = b""
    for f in sorted(schema_dir.iterdir()):
        if f.is_file():
            schema_bytes += f.read_bytes()
    recomputed = hashlib.sha256(vectors + invariants + schema_bytes).hexdigest()
    assert recomputed == m["checksum"], (
        f"checksum 复算不一致：manifest={m['checksum']} recomputed={recomputed}"
    )


def test_golden_vectors_fingerprint_uuidv5_relation():
    """黄金向量的 fingerprint 与 UUIDv5 关系真实可复算（Bugbot B1/B6）。"""
    import sys
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    from contracts_identity import (
        FROZEN_EVIDENCE_NAMESPACE, canonical_provenance_fields, provenance_fingerprint,
    )

    v = json.loads((_bundle_dir() / "conformance-vectors.json").read_text())
    for gv in v["golden_fingerprint_vectors"]:
        inp = gv["input"]
        # 用与 contracts_identity 完全一致的规范化重算 fingerprint
        fp = provenance_fingerprint(
            canonical_provenance_fields(
                source=inp["source"],
                query_id=inp.get("query_id", ""),
                resource_id=inp.get("resource_id", ""),
                time_range_start=inp.get("time_range_start"),
                time_range_end=inp.get("time_range_end"),
                digest=inp.get("digest", ""),
                tenant_id=inp.get("tenant_id", ""),
                cluster_id=inp.get("cluster_id", ""),
                run_id=inp.get("run_id", ""),
            )
        )
        assert fp == gv["expected_fingerprint"], f"{gv['name']} fingerprint 不匹配"
        # UUIDv5(NS, fingerprint) == expected_evidence_id
        eid = uuid.uuid5(FROZEN_EVIDENCE_NAMESPACE, fp)
        assert str(eid) == gv["expected_evidence_id"], f"{gv['name']} evidence_id 不匹配"


def test_golden_plan_step_id_vectors():
    """PlanStep id 黄金向量可复算（Y2：UUIDv5(NS, v1\\0run\\0plan\\0label)，跨语言一致）。"""
    import sys
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
    from contracts_identity import FROZEN_PLAN_STEP_NS, plan_step_id

    v = json.loads((_bundle_dir() / "conformance-vectors.json").read_text())
    assert str(FROZEN_PLAN_STEP_NS) == v["frozen_plan_step_namespace"]
    for gv in v["golden_plan_step_id_vectors"]:
        eid = plan_step_id(gv["run_id"], gv["plan_id"], gv["label"])
        assert str(eid) == gv["expected_step_id"], f"plan step {gv['label']} id 不匹配"


def test_contracts_py_is_authoritative_binding():
    # contracts.py 是权威 Python binding（单一权威，禁止新建第二合同层/contracts/ 包）
    import contracts
    assert hasattr(contracts, "ContractModel")
    assert hasattr(contracts, "ToolResult")
    assert hasattr(contracts, "Evidence")
    assert hasattr(contracts, "Hypothesis")
    assert hasattr(contracts, "PlanStep")
    assert hasattr(contracts, "ToolStatus")
    assert hasattr(contracts, "PlanStepStatus")
    # V1 冻结 ToolResult 恰好 15 字段
    assert len(contracts.ToolResult.model_fields) == 15
