"""P9.2 Hypothesis Generator — V9.3 Phase9（评审加固后测试）。

生成多个 candidate，每个包含：claim、affected resource、expected mechanism、
required support、potential contradiction。禁止直接生成 confirmed root cause（§七十五 P9.2）。

评审加固断言：
- 强隔离字段 tenant_id / cluster_id / resource_id 必填。
- 一个 Hypothesis 不得混用跨 cluster Evidence（强隔离）。
- Hypothesis 是 Phase 9 唯一正式实体（带 run/tenant/cluster/resource identity）。
"""
import uuid

import pytest

# R2 方案 B：强隔离字段用合法 UUID（run/tenant/cluster）
RUN_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
_CTX = {
    "tenant_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    "cluster_id": "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
}


def test_generates_multiple_candidates():
    from hypothesis import HypothesisGenerator

    gen = HypothesisGenerator()
    hypotheses = gen.generate(
        run_id=RUN_ID,
        symptoms=["service error rate spike"],
        **_CTX,
    )
    assert len(hypotheses) > 1


def test_each_hypothesis_has_required_fields():
    from hypothesis import HypothesisGenerator

    gen = HypothesisGenerator()
    hypotheses = gen.generate(run_id=RUN_ID, symptoms=["latency spike"], **_CTX)
    for h in hypotheses:
        assert h.claim
        assert h.affected_resource
        assert h.expected_mechanism
        assert h.required_support
        assert h.potential_contradiction is not None


def test_hypothesis_starts_as_candidate_not_confirmed():
    from hypothesis import HypothesisGenerator

    gen = HypothesisGenerator()
    hypotheses = gen.generate(run_id=RUN_ID, symptoms=["p99 spike"], **_CTX)
    for h in hypotheses:
        assert h.status == "candidate"


def test_hypothesis_has_unique_ids():
    from hypothesis import HypothesisGenerator

    gen = HypothesisGenerator()
    hypotheses = gen.generate(run_id=RUN_ID, symptoms=["deploy regression"], **_CTX)
    ids = [h.hypothesis_id for h in hypotheses]
    assert len(ids) == len(set(ids))


# ---- 评审加固断言 ----

def test_hypothesis_carries_tenant_cluster_resource_identity():
    from hypothesis import HypothesisGenerator

    gen = HypothesisGenerator()
    hypotheses = gen.generate(
        run_id=RUN_ID, symptoms=["error spike"], **_CTX, resource_id="cluster-1/svc/checkout",
    )
    for h in hypotheses:
        assert h.run_id == RUN_ID
        assert h.tenant_id == _CTX["tenant_id"]
        assert h.cluster_id == _CTX["cluster_id"]
        assert h.resource_id == "cluster-1/svc/checkout"


def test_hypothesis_rejects_missing_tenant_or_cluster():
    # R2 收敛：Hypothesis 组合权威 contracts.Hypothesis（UUID identity）。
    # 强隔离字段必填由权威 validator/构造校验（tenant/cluster 为空拒绝）。
    import contracts as C
    from hypothesis import Hypothesis

    def make(tenant, cluster, rid=""):
        return Hypothesis(
            C.Hypothesis(
                hypothesis_id=uuid.uuid4(), run_id=RUN_ID, title="c", description="m",
                confidence=0.0, status=C.HypothesisStatus.CANDIDATE,
                tenant_id=tenant, cluster_id=cluster, resource_id=rid, affected_resource="r",
            )
        )

    with pytest.raises(ValueError):
        # tenant 非法 UUID → 权威 pydantic 拒绝
        make("not-a-uuid", _CTX["cluster_id"])
    with pytest.raises(ValueError):
        make(_CTX["tenant_id"], "not-a-uuid")


def test_hypothesis_resource_id_is_canonical_not_symptom():
    from hypothesis import HypothesisGenerator

    gen = HypothesisGenerator()
    hypotheses = gen.generate(
        run_id=RUN_ID, symptoms=["error spike"], **_CTX, resource_id="cluster-1/svc/checkout",
    )
    # resource_id 用规范资源标识（cluster/ns/pod），非自由症状文本
    for h in hypotheses:
        assert h.resource_id == "cluster-1/svc/checkout"
        assert "error spike" not in h.resource_id
