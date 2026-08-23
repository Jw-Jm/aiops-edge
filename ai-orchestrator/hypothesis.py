"""P9.2 Hypothesis Generator — V9.3 Phase9（评审加固版）。

生成多个 candidate，每个包含：claim、affected resource、expected mechanism、
required support、potential contradiction。禁止直接生成 confirmed root cause（§七十五 P9.2）。

评审修复（2026-08-21 Gate 9 FAIL 退回）：
- Hypothesis 强隔离字段：tenant_id / cluster_id / resource_id 强制携带。
- affected_resource 不再塞症状文本；改用规范 resource_id（cluster/service/pod 等）。
- 单一正式 Hypothesis 实体（Phase 9 RCA 唯一使用），不得与 investigation_state.Hypothesis 混用产生歧义。
- 禁止直接 confirmed：status 起始 candidate，最终由 P9.8 Ranker 判定。

关键约束：
- 生成多个 candidate（>1），每轮 RCA 不预设唯一答案。
- 不输出 final root cause。
- 最终 confirmed 判定完全由公式决定，LLM 不能直接标记 confirmed。
"""
from __future__ import annotations

from typing import Any, List
from uuid import UUID

import contracts

class HypothesisError(ValueError):
    def __init__(self, message: str):
        self.error_code = "HYPOTHESIS_ERROR"
        super().__init__(message)


HYPOTHESIS_STATUSES = {"candidate", "supported", "confirmed", "rejected", "unknown"}


class Hypothesis:
    """因果候选（非事实、非 confirmed）。Phase 9 RCA 唯一正式 Hypothesis 实体（R2 收敛：组合权威）。

    强隔离：
    - run_id / tenant_id / cluster_id / resource_id 必填，一个 Hypothesis 不得混用跨 cluster Evidence。
    - resource_id 是规范资源标识（如 cluster/svc 或 cluster/ns/pod），非自由文本。
    - status 由 P9.8 Ranker 判定，不由生成器预设。

    R2 收敛：组合权威 contracts.Hypothesis（UUID identity + 强隔离字段），
    required_support/potential_contradiction（类型名）保留封装层（权威 supporting/contradicting_evidence 是 UUID 引用）。
    """

    __slots__ = ("contract", "required_support", "potential_contradiction", "status")

    def __init__(self, contract, required_support=None, potential_contradiction=None,
                 status: str = "candidate"):
        if status not in HYPOTHESIS_STATUSES:
            raise HypothesisError(f"非法 status: {status}")
        object.__setattr__(self, "contract", contract)
        object.__setattr__(self, "required_support", list(required_support or []))
        object.__setattr__(self, "potential_contradiction", list(potential_contradiction or []))
        object.__setattr__(self, "status", status)

    # ── 权威字段映射转发 ────────────────────────────────────────────────
    @property
    def claim(self) -> str:
        return self.contract.title

    @property
    def expected_mechanism(self) -> str:
        return self.contract.description

    @property
    def hypothesis_id(self) -> str:
        return str(self.contract.hypothesis_id)

    @property
    def run_id(self) -> str:
        return str(self.contract.run_id)

    @property
    def affected_resource(self) -> str:
        return self.contract.affected_resource

    @property
    def tenant_id(self) -> str:
        return str(self.contract.tenant_id) if self.contract.tenant_id else ""

    @property
    def cluster_id(self) -> str:
        return str(self.contract.cluster_id) if self.contract.cluster_id else ""

    @property
    def resource_id(self) -> str:
        return self.contract.resource_id

    @property
    def id(self) -> UUID:
        return self.contract.hypothesis_id

    def __getattr__(self, name: str) -> Any:
        c = object.__getattribute__(self, "contract")
        if name in type(c).model_fields:
            val = getattr(c, name)
            if isinstance(val, UUID):
                return str(val)
            return val
        raise AttributeError(f"Hypothesis 无字段 {name!r}")


class HypothesisGenerator:
    """生成多个 RCA candidate（基于症状的领域模板候选，状态恒为 candidate）。"""

    # 症状关键词 → (claim 模板, expected_mechanism, required_support, potential_contradiction)
    _TEMPLATES = {
        "error_rate": {
            "claim": "服务错误率上升由后端依赖失败导致",
            "mechanism": "下游调用失败 → 上游重试/5xx 堆积",
            "support": ["metric_anomaly", "log_error", "trace_anomaly"],
            "contradiction": ["metric_log_trace_conflict"],
        },
        "latency": {
            "claim": "P99 延迟上升由资源争用或锁竞争导致",
            "mechanism": "CPU/内存/连接池争用 → 请求排队",
            "support": ["metric_anomaly", "resource_state", "trace_anomaly"],
            "contradiction": ["temporal_relation_weak"],
        },
        "deploy": {
            "claim": "异常由最近变更发布导致",
            "mechanism": "新版本行为变化 → 行为回归",
            "support": ["change", "log_pattern"],
            "contradiction": ["change_after_fault"],
        },
        "default": {
            "claim": "异常由资源或配置状态异常导致",
            "mechanism": "资源状态偏离期望 → 能力下降",
            "support": ["resource_state", "k8s_state"],
            "contradiction": ["resource_cluster_conflict"],
        },
    }

    def generate(
        self,
        run_id: str,
        symptoms: List[str],
        *,
        tenant_id: str,
        cluster_id: str,
        resource_id: str = "",
    ) -> List[Hypothesis]:
        """根据症状生成多个 candidate（>1），带强隔离字段。"""
        if not symptoms:
            return [self._from_template(run_id, "default", "", tenant_id, cluster_id, resource_id)]
        candidates: List[Hypothesis] = []
        for symptom in symptoms:
            key = self._match(symptom)
            candidates.append(
                self._from_template(run_id, key, symptom, tenant_id, cluster_id, resource_id)
            )
        # 至少 2 个 candidate，避免预设唯一答案
        if len(candidates) < 2:
            candidates.append(
                self._from_template(run_id, "default", "", tenant_id, cluster_id, resource_id)
            )
        return candidates

    def _match(self, symptom: str) -> str:
        low = symptom.lower()
        if "error" in low or "5xx" in low:
            return "error_rate"
        if "latency" in low or "p99" in low or "slow" in low:
            return "latency"
        if "deploy" in low or "release" in low or "rollout" in low or "version" in low:
            return "deploy"
        return "default"

    def _from_template(
        self, run_id: str, key: str, symptom: str,
        tenant_id: str, cluster_id: str, resource_id: str,
    ) -> Hypothesis:
        t = self._TEMPLATES[key]
        contract = contracts.Hypothesis(
            hypothesis_id=_hypothesis_uuid(run_id, key, symptom),
            run_id=_as_uuid(run_id),
            title=t["claim"],
            description=t["mechanism"],
            supporting_evidence=[],           # UUID 引用由调用方填，类型名在 required_support
            contradicting_evidence=[],
            missing_evidence=[],
            confidence=0.0,
            status=contracts.HypothesisStatus.CANDIDATE,
            tenant_id=_as_uuid(tenant_id) if tenant_id else None,
            cluster_id=_as_uuid(cluster_id) if cluster_id else None,
            resource_id=resource_id,
            affected_resource=symptom or resource_id or "cluster/service",
        )
        return Hypothesis(
            contract,
            required_support=list(t["support"]),
            potential_contradiction=list(t["contradiction"]),
            status="candidate",
        )


def _hypothesis_uuid(run_id: str, key: str, symptom: str) -> UUID:
    """确定性 hypothesis_id = UUIDv5(FROZEN_PLAN_STEP_NS, 'hypo:'+run_id+':'+key+':'+symptom)。

    R2 收敛：hypothesis_id 由 SHA16 → 权威 UUID（可复现、可持久化）。
    """
    import uuid as _uuid
    from contracts_identity import FROZEN_PLAN_STEP_NS
    return _uuid.uuid5(FROZEN_PLAN_STEP_NS, f"hypo:{run_id}:{key}:{symptom}")


def _as_uuid(value: Any) -> UUID:
    if isinstance(value, UUID):
        return value
    try:
        return UUID(str(value))
    except (ValueError, TypeError):
        from contracts_identity import FROZEN_PLAN_STEP_NS
        import uuid as _uuid
        return _uuid.uuid5(FROZEN_PLAN_STEP_NS, f"id:{value}")
