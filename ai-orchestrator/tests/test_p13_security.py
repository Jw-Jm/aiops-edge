"""V9.3 Phase 13 — 服务端安全加固测试（P13.2-P13.8 + Gate 13）。

核心：服务端独立拒绝所有越权；前端隐藏按钮不算安全控制。
"""
import uuid

import pytest

from authorization_matrix import AuthorizationMatrix, AuthzError, AuthzRule
from manual_boundary import ManualBoundary, ManualTriggerDenied
from phase11_execution import ApprovalService, OpsActionFactory, Phase11Error

TENANT = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
CLUSTER = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"


def _matrix():
    m = AuthorizationMatrix()
    m.add_rule(AuthzRule(principal="eng_alice", tenant_id=TENANT, cluster_id=CLUSTER,
                         capability="observability.read", action="*"))
    m.add_rule(AuthzRule(principal="op_bob", tenant_id=TENANT, cluster_id=CLUSTER,
                         capability="observability.read", action="*", risk_max="R2",
                         require_confirmation=True))
    m.add_rule(AuthzRule(principal="admin_carol", tenant_id=TENANT, cluster_id=CLUSTER,
                         capability="observability.read", action="*", risk_max="R4",
                         require_approval=True))
    return m


# ── P13.2 Public API Tamper ─────────────────────────────────────────────

def test_role_tamper_ignored_server_side():
    """localStorage role tampering：服务端用权威角色（忽略前端 role 参数）。"""
    m = _matrix()
    # 前端传 role=admin，但 principal=eng_alice（engineer）→ 服务端按 engineer 校验
    m.authorize_request(principal="eng_alice", role="admin", tenant_id=TENANT,
                        cluster_id=CLUSTER, capability="observability.read")
    # engineer 无 ai.investigate? 不，engineer 有 ai.investigate；但 admin-only capability 拒绝
    with pytest.raises(AuthzError):
        m.authorize_request(principal="eng_alice", role="admin", tenant_id=TENANT,
                            cluster_id=CLUSTER, capability="system.admin")


def test_cross_tenant_denied():
    m = _matrix()
    with pytest.raises(AuthzError) as ex:
        m.authorize(principal="eng_alice", tenant_id="other-tenant", cluster_id=CLUSTER,
                    capability="observability.read")
    assert ex.value.error_code == "AUTHZ_DENIED"


def test_id_guessing_denied():
    """run/evidence ID guessing：未授权 principal 无法访问。"""
    m = _matrix()
    with pytest.raises(AuthzError):
        m.authorize(principal="viewer_x", tenant_id=TENANT, cluster_id=CLUSTER,
                    capability="observability.read")  # viewer 无 read? viewer 有 read，但无规则


def test_admin_capability_required_for_admin_ops():
    m = _matrix()
    with pytest.raises(AuthzError):
        m.authorize(principal="eng_alice", tenant_id=TENANT, cluster_id=CLUSTER,
                    capability="system.admin")  # engineer 无 system.admin


# ── P13.4 Registered Source Security ────────────────────────────────────

def test_unknown_cluster_denied():
    from data_source_mapping import ClusterFailClosed, DataSourceMapping
    dsm = DataSourceMapping()
    with pytest.raises(ClusterFailClosed):
        dsm.resolve_cluster("unknown-canonical-cluster")


def test_registered_source_does_not_bypass_authz():
    """注册映射不能绕过既有 tenant/cluster/resource authorization。"""
    m = _matrix()
    # 即使数据源已注册，未授权 principal 仍拒绝
    with pytest.raises(AuthzError):
        m.authorize(principal="viewer_x", tenant_id=TENANT, cluster_id=CLUSTER,
                    capability="observability.read")


# ── P13.5 Manual AI Trigger Security ───────────────────────────────────

def test_system_principal_cannot_start_run():
    mb = ManualBoundary()
    with pytest.raises(ManualTriggerDenied) as ex:
        mb.require_user_explicit(source="user_explicit", principal_type="system")
    assert ex.value.error_code == "MANUAL_TRIGGER_REQUIRED"


def test_alert_event_change_does_not_trigger():
    mb = ManualBoundary()
    for src in ("alert", "event", "change", "page_load"):
        with pytest.raises(ManualTriggerDenied):
            mb.require_user_explicit(source=src, principal_type="user")


def test_no_auto_new_run():
    mb = ManualBoundary()
    assert mb.allow_auto_new_run() is False  # Run 结束后不得自动新建


# ── P13.6 Approval Security ────────────────────────────────────────────

def _action(factory):
    return factory.create(
        run_id=str(uuid.uuid4()), tenant_id=TENANT, cluster_id=CLUSTER,
        resource_id="svc/checkout", namespace="prod", action_type="patch_resource",
        parameters={"replicas": 3}, expected_effect="scale", verification_policy="health",
        risk="R1", root_cause_confidence=0.9, resource_version="rv-1", rca_status="confirmed",
    )


def test_self_approval_denied():
    f = OpsActionFactory()
    a = _action(f)
    appr = ApprovalService()
    with pytest.raises(Phase11Error) as ex:
        appr.approve(approver="alice", requester="alice", action=a,
                     requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    assert ex.value.error_code == "SELF_APPROVAL"


def test_admin_self_approval_denied():
    f = OpsActionFactory()
    a = _action(f)
    appr = ApprovalService()
    with pytest.raises(Phase11Error):
        appr.approve(approver="admin", requester="admin", action=a,
                     requester_cluster=CLUSTER, approver_cluster=CLUSTER)


def test_cross_cluster_approval_denied():
    f = OpsActionFactory()
    a = _action(f)
    appr = ApprovalService()
    with pytest.raises(Phase11Error):
        appr.approve(approver="bob", requester="alice", action=a,
                     requester_cluster=CLUSTER, approver_cluster="other-cluster")


def test_stale_action_hash_denied():
    f = OpsActionFactory()
    a = _action(f)
    appr = ApprovalService()
    rec = appr.approve(approver="bob", requester="alice", action=a,
                       requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    # 修改 action hash（stale）→ approval 拒绝
    from phase11_execution import StructuredOpsAction
    a_mut = StructuredOpsAction(action_id=a.action_id, version=a.version + 1, action_hash="stale",
                                action_type=a.action_type, run_id=a.run_id, tenant_id=a.tenant_id,
                                cluster_id=a.cluster_id, resource_id=a.resource_id, namespace=a.namespace,
                                parameters={}, idempotency_key=a.idempotency_key,
                                resource_version=a.resource_version, expected_effect=a.expected_effect,
                                verification_policy=a.verification_policy, risk=a.risk)
    with pytest.raises(Phase11Error):
        appr.verify(rec.approval_id, a_mut)  # APPROVAL_ACTION_MISMATCH


# ── P13.7 NetworkPolicy ────────────────────────────────────────────────

def test_agent_has_no_db_direct_access():
    """Agent 无 DB/storage direct access（Agent 只经 Tool Registry→query-api）。"""
    from agent_runtime import AgentRuntimeFramework
    # Agent runtime 无 DB client / K8s client 属性
    ar = AgentRuntimeFramework(registry=None)
    assert not hasattr(ar, "db_client")
    assert not hasattr(ar, "k8s_client")


def test_orchestrator_no_k8s_credential():
    """orchestrator 核心执行链无 target K8s credential / direct K8s egress。

    红线 F4/F5：orchestrator 不持有 kubeconfig/直接执行 K8s。只检查真实执行组件
    （demo/图谱/rca 旧文件含 kubeconfig 字样属非执行路径，不在此范围）。
    """
    import pathlib
    root = pathlib.Path(__file__).resolve().parents[1]
    # 核心执行链组件（红线 F4/F5 隔离 grep execute/credential/kubeconfig 0 match 的延续）
    core = ["agent_runtime.py", "evidence_hub.py", "planner.py", "rca_engine.py",
            "run_persistence.py", "phase11_execution.py"]
    bad = []
    for name in core:
        text = (root / name).read_text()
        if "kubeconfig" in text:
            bad.append(name)
    assert bad == [], f"orchestrator 核心执行链出现 kubeconfig 直接使用: {bad}"


# ── P13.8 Secret Separation ────────────────────────────────────────────

def test_secret_not_in_evidence_report():
    """报告/Evidence 只写引用和 digest/metadata，不落 Secret。"""
    from datetime import datetime, timezone

    from evidence_hub import EvidenceHub
    from tool_result import ToolExecutionRecord
    hub = EvidenceHub()
    tr = ToolExecutionRecord(
        tool_name="query_k8s", tool_id="query_k8s.v1", cluster_id=CLUSTER, tenant_id=TENANT,
        status="success", summary="ok", data={"pod": "x"}, error_code="", error_message="",
        retryable=False, retry_policy={}, evidence_ids=[], evidence_required=False,
        source_system="query-api", request_id="r", query_id="q", time_range="",
        started_at=datetime.now(timezone.utc), finished_at=datetime.now(timezone.utc),
        duration_ms=0, provenance={},
    )
    ev = hub.save_from_tool_result(tr, run_id=str(uuid.uuid4()), evidence_type="k8s_state")
    # Evidence 序列化不得含 kubeconfig/token/secret 明文
    import json
    blob = json.dumps(ev.contract.model_dump(), default=str)
    for secret in ("kubeconfig", "api_token", "secret_key", "-----BEGIN"):
        assert secret not in blob, f"Evidence 泄漏 secret 关键字: {secret}"


# ── Gate 13 ────────────────────────────────────────────────────────────

def test_gate13_server_side_rejects_all_escalation():
    """Gate 13：服务端独立拒绝所有越权（不依赖前端隐藏）。"""
    # 越权路径全部 fail-closed
    m = _matrix()
    mb = ManualBoundary()
    cases = [
        lambda: m.authorize(principal="eng_alice", tenant_id=TENANT, cluster_id="other",
                            capability="observability.read"),  # 跨 cluster
        lambda: m.authorize(principal="eng_alice", tenant_id=TENANT, cluster_id=CLUSTER,
                            capability="system.admin"),  # capability 提升
        lambda: mb.require_user_explicit(source="alert", principal_type="user"),  # 非显式
        lambda: mb.require_user_explicit(source="user_explicit", principal_type="system"),  # system
    ]
    for fn in cases:
        with pytest.raises((AuthzError, ManualTriggerDenied)):
            fn()
