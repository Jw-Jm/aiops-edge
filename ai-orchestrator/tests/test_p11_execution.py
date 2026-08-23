"""V9.3 Phase 11 — Remediation/Approval/Execution 测试（P11.1-P11.10 + Gate 11）。"""
import uuid

import pytest

from phase11_execution import (
    ApprovalService,
    AuthoritativeRiskEngine,
    ConfirmationService,
    ExecutionAdapter,
    OpsActionFactory,
    Phase11Error,
    Precheck,
    RegressionStop,
    RollbackService,
    StructuredOpsAction,
    Verification,
)

RUN = str(uuid.uuid4())
TENANT = str(uuid.uuid4())
CLUSTER = str(uuid.uuid4())


def _action(factory, **kw):
    kw.setdefault("run_id", RUN)
    kw.setdefault("tenant_id", TENANT)
    kw.setdefault("cluster_id", CLUSTER)
    kw.setdefault("resource_id", "svc/checkout")
    kw.setdefault("namespace", "prod")
    kw.setdefault("action_type", "patch_resource")
    kw.setdefault("parameters", {"replicas": 3})
    kw.setdefault("expected_effect", "scale to 3")
    kw.setdefault("verification_policy", "error_rate < 1%")
    kw.setdefault("risk", "R1")
    kw.setdefault("root_cause_confidence", 0.9)
    kw.setdefault("resource_version", "rv-1")
    kw.setdefault("rca_status", "confirmed")
    return factory.create(**kw)


# ── P11.1 OpsAction Factory ─────────────────────────────────────────────

def test_factory_creates_complete_action():
    f = OpsActionFactory()
    a = _action(f)
    assert a.action_id and a.action_hash and a.idempotency_key
    assert a.resource_version == "rv-1"
    assert a.expected_effect and a.verification_policy
    assert a.automatic is False  # 禁自动执行


def test_factory_rejects_unready_rca():
    f = OpsActionFactory()
    with pytest.raises(Phase11Error) as ex:
        _action(f, rca_status="candidate")
    assert ex.value.error_code == "RCA_NOT_READY"


def test_factory_rejects_illegal_action_type():
    f = OpsActionFactory()
    with pytest.raises(Phase11Error):
        _action(f, action_type="drop_table")


# ── P11.2 Authoritative Risk Engine ─────────────────────────────────────

def test_risk_not_below_baseline():
    eng = AuthoritativeRiskEngine(baseline="R2")
    a = _action(OpsActionFactory())
    risk = eng.compute(action=a, rca_confidence=0.9, blast_radius="single", environment="prod")
    assert risk in ("R2", "R3", "R4")  # 不低于 baseline R2


def test_low_confidence_raises_risk():
    eng = AuthoritativeRiskEngine(baseline="R0")
    a = _action(OpsActionFactory())
    assert eng.compute(action=a, rca_confidence=0.3, blast_radius="single", environment="prod") == "R3"


def test_restricted_shell_is_R4():
    eng = AuthoritativeRiskEngine(baseline="R0")
    a = _action(OpsActionFactory(), action_type="restricted_shell")
    risk = eng.compute(action=a, rca_confidence=0.9, blast_radius="single", environment="prod")
    assert risk == "R4"
    assert a.planner_selectable is False  # P11.7 restricted_shell 不可 planner selectable


def test_no_l0_l4_autonomy_model():
    """Gate 11：不引入 L0-L4 Autonomy 风险语义。"""
    eng = AuthoritativeRiskEngine()
    assert hasattr(eng, "compute")
    assert not hasattr(eng, "autonomy_level")


# ── P11.3 Confirmation ─────────────────────────────────────────────────

def test_confirmation_binds_action_fields():
    f = OpsActionFactory()
    a = _action(f)
    svc = ConfirmationService()
    c = svc.confirm(requester="alice", action=a)
    svc.verify_binding(c.confirmation_id, a)  # 未变更 → OK


def test_action_mutation_invalidates_confirmation():
    f = OpsActionFactory()
    a = _action(f)
    svc = ConfirmationService()
    c = svc.confirm(requester="alice", action=a)
    # 修改 action（新参数 → 新 hash）→ 需重确认
    a_mut = StructuredOpsAction(
        action_id=a.action_id, version=a.version + 1,
        action_hash="changed", action_type=a.action_type, run_id=a.run_id,
        tenant_id=a.tenant_id, cluster_id=a.cluster_id, resource_id=a.resource_id,
        namespace=a.namespace, parameters={"replicas": 5}, idempotency_key=a.idempotency_key,
        resource_version=a.resource_version, expected_effect="different", verification_policy=a.verification_policy,
        risk=a.risk,
    )
    with pytest.raises(Phase11Error) as ex:
        svc.verify_binding(c.confirmation_id, a_mut)
    assert ex.value.error_code == "ACTION_MUTATED_NEEDS_RE_CONFIRM"


# ── P11.4 Approval ─────────────────────────────────────────────────────

def test_approval_requires_distinct_approver():
    f = OpsActionFactory()
    a = _action(f)
    svc = ApprovalService()
    with pytest.raises(Phase11Error) as ex:
        svc.approve(approver="alice", requester="alice", action=a,
                    requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    assert ex.value.error_code == "SELF_APPROVAL"  # admin 也不例外


def test_approval_rejects_cross_cluster():
    f = OpsActionFactory()
    a = _action(f)
    svc = ApprovalService()
    with pytest.raises(Phase11Error) as ex:
        svc.approve(approver="bob", requester="alice", action=a,
                    requester_cluster=CLUSTER, approver_cluster="other-cluster")
    assert ex.value.error_code == "CROSS_CLUSTER_APPROVAL"


def test_approval_binds_action_identity():
    f = OpsActionFactory()
    a = _action(f)
    svc = ApprovalService()
    rec = svc.approve(approver="bob", requester="alice", action=a,
                      requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    svc.verify(rec.approval_id, a)  # 未变更 → OK
    a_mut = StructuredOpsAction(action_id=a.action_id, version=2, action_hash="x",
                                action_type=a.action_type, run_id=a.run_id, tenant_id=a.tenant_id,
                                cluster_id=a.cluster_id, resource_id=a.resource_id, namespace=a.namespace,
                                parameters={}, idempotency_key=a.idempotency_key,
                                resource_version=a.resource_version, expected_effect=a.expected_effect,
                                verification_policy=a.verification_policy, risk=a.risk)
    with pytest.raises(Phase11Error):
        svc.verify(rec.approval_id, a_mut)  # approval 绑定的 identity 变更


# ── P11.5 Precheck ─────────────────────────────────────────────────────

def test_precheck_fails_on_resource_drift():
    f = OpsActionFactory()
    a = _action(f)
    pc = Precheck()
    with pytest.raises(Phase11Error) as ex:
        pc.verify(action=a, current_resource_version="rv-2", current_health="healthy")
    assert ex.value.error_code == "PRECHECK_RESOURCE_VERSION"


def test_precheck_fails_on_maintenance_or_conflict():
    f = OpsActionFactory()
    a = _action(f)
    pc = Precheck()
    with pytest.raises(Phase11Error):
        pc.verify(action=a, current_resource_version="rv-1", current_health="healthy", maintenance=True)
    with pytest.raises(Phase11Error):
        pc.verify(action=a, current_resource_version="rv-1", current_health="healthy", conflicting_action=True)


# ── P11.6/P11.7 Execution Adapter ──────────────────────────────────────

def test_adapter_rejects_raw_shell_as_fallback():
    f = OpsActionFactory()
    a = _action(f, action_type="restricted_shell")
    ad = ExecutionAdapter()
    with pytest.raises(Phase11Error) as ex:
        ad.execute(action=a, resource_type="svc", before_state={"replicas": 2})
    assert ex.value.error_code == "ADAPTER_NO_SHELL"


def test_adapter_patch_enforces_field_allowlist():
    f = OpsActionFactory()
    a = _action(f, parameters={"replicas": 3, "evil_field": 1})
    ad = ExecutionAdapter()
    ad.register_patch_fields("svc", {"replicas"})
    with pytest.raises(Phase11Error) as ex:
        ad.execute(action=a, resource_type="svc", before_state={"replicas": 2})
    assert ex.value.error_code == "ADAPTER_PATCH_FORBIDDEN"


def test_adapter_executes_structured_patch():
    f = OpsActionFactory()
    a = _action(f, parameters={"replicas": 3})
    ad = ExecutionAdapter()
    ad.register_patch_fields("svc", {"replicas"})
    after = ad.execute(action=a, resource_type="svc", before_state={"replicas": 2})
    assert after["replicas"] == 3
    assert a.action_id in ad.executed_ids()


# ── P11.8 Verification（SLI 非 exit code）──────────────────────────────

def test_verification_uses_health_not_exit_code():
    v = Verification()
    a = _action(OpsActionFactory())
    # exit_code=0 但 health 未恢复 → partial（不是 success）
    verdict = v.verify(action=a, before_health=0.5, after_health=0.6, exit_code=0, sli_threshold=0.8)
    assert verdict == "partial"
    # exit_code=1 → failed（退出码只是 fact）
    verdict2 = v.verify(action=a, before_health=0.9, after_health=0.95, exit_code=1, sli_threshold=0.8)
    assert verdict2 == "failed"


def test_verification_regressed_on_health_degradation():
    v = Verification()
    a = _action(OpsActionFactory())
    verdict = v.verify(action=a, before_health=0.9, after_health=0.3, exit_code=0, sli_threshold=0.8)
    assert verdict == "regressed"


# ── P11.9 Regression Stop ──────────────────────────────────────────────

def test_regression_stop_blocks_further_actions():
    rs = RegressionStop()
    rs.mark_regressed(RUN)
    with pytest.raises(Phase11Error) as ex:
        rs.assert_action_allowed(RUN)
    assert ex.value.error_code == "REGRESSION_STOP"


# ── P11.10 Rollback as New Action ──────────────────────────────────────

def test_rollback_is_new_action():
    f = OpsActionFactory()
    a = _action(f, risk="R1")
    rb = RollbackService(f).create_rollback(original=a, before_state={"replicas": 2})
    assert rb.action_id != a.action_id  # 新 action_id
    assert rb.action_type == "rollback"
    assert rb.action_hash != a.action_hash  # 新 hash
    assert rb.version == 1


# ── Gate 11 断言 ───────────────────────────────────────────────────────

def test_gate11_human_gates_not_bypassed():
    """R2/R3/R4 human gates 不能绕过：confirmation + approval 都是必要条件。"""
    f = OpsActionFactory()
    a = _action(f)
    conf = ConfirmationService()
    appr = ApprovalService()
    # 无 confirmation → ACTION_MUTATED 或无法执行
    c = conf.confirm(requester="alice", action=a)
    conf.verify_binding(c.confirmation_id, a)
    rec = appr.approve(approver="bob", requester="alice", action=a,
                       requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    appr.verify(rec.approval_id, a)
    # 若缺 approver → verify 抛（approval 不存在）
    with pytest.raises(Phase11Error):
        appr.verify("missing", a)


def test_gate11_verification_uses_sli_not_exit_code():
    v = Verification()
    a = _action(OpsActionFactory())
    # exit_code=0 但未达 SLI → 非 success（证明 verdict 依赖 health 而非 exit code）
    assert v.verify(action=a, before_health=0.4, after_health=0.5, exit_code=0, sli_threshold=0.8) != "success"
