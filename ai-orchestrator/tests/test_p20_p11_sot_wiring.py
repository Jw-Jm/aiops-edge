"""P20 Plan1 Task5 — P11 ApprovalService 接 query-api 权威 SoT（fail-closed）。

验证 ApprovalService 当配置 AuthorizationSoTProvider 时：
- cluster 未启用授权 → fail-closed 拒绝（APPROVAL_SOT_UNAVAILABLE）。
- SoT 不可达（provider 抛异常）→ fail-closed 拒绝，不降级。
- approver/requester 无对应 capability → 拒绝。
- SoT 授权齐全 → 放行。
- 未配置 provider（In-memory MVP）→ 保持既有行为（放行）。

对应 ledger：P20-BUGBOT-P1-04（P11 未接线 → 本任务接线验证）。
"""
from __future__ import annotations

import pytest

from phase11_execution import ApprovalService, OpsActionFactory, Phase11Error, StructuredOpsAction

CLUSTER = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"


class _FakeSoT:
    """模拟 AuthorizationSoTProvider（load_authorization）。"""

    def __init__(self, authz: dict | None = None, *, fail: bool = False) -> None:
        self._authz = authz or {}
        self._fail = fail

    def load_authorization(self, cluster_id: str) -> dict:
        if self._fail:
            raise RuntimeError("SoT backend unavailable")
        return dict(self._authz)


def _action() -> StructuredOpsAction:
    f = OpsActionFactory()
    return f.create(
        run_id="aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
        tenant_id="7ed01afc-cc79-4ecd-8767-a2befa6168ad",
        cluster_id=CLUSTER,
        resource_id="svc/checkout",
        namespace="prod",
        action_type="patch_resource",
        parameters={"replicas": 3},
        expected_effect="scale to 3",
        verification_policy="error_rate < 1%",
        risk="R1",
        root_cause_confidence=0.9,
        resource_version="rv-1",
        rca_status="confirmed",
    )


def test_sot_unavailable_cluster_fail_closed():
    # cluster 未启用授权 → 拒绝
    svc = ApprovalService(sot_provider=_FakeSoT({"enabled": False}))
    with pytest.raises(Phase11Error) as ex:
        svc.approve(approver="bob", requester="alice", action=_action(),
                    requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    assert ex.value.error_code == "APPROVAL_SOT_UNAVAILABLE"


def test_sot_backend_down_fail_closed():
    # SoT 不可达（抛异常）→ fail-closed 拒绝，不降级
    svc = ApprovalService(sot_provider=_FakeSoT(fail=True))
    with pytest.raises(Phase11Error) as ex:
        svc.approve(approver="bob", requester="alice", action=_action(),
                    requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    assert ex.value.error_code == "APPROVAL_SOT_UNAVAILABLE"


def test_sot_approver_lacks_approve_capability():
    # approver 无 action.approve → 拒绝
    svc = ApprovalService(sot_provider=_FakeSoT({
        "enabled": True, "capabilities": ["action.confirm"],
    }))
    with pytest.raises(Phase11Error) as ex:
        svc.approve(approver="bob", requester="alice", action=_action(),
                    requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    assert ex.value.error_code == "APPROVAL_SOT_UNAVAILABLE"


def test_sot_requester_lacks_confirm_capability():
    # requester 无 action.confirm → 拒绝
    svc = ApprovalService(sot_provider=_FakeSoT({
        "enabled": True, "capabilities": ["action.approve"],
    }))
    with pytest.raises(Phase11Error) as ex:
        svc.approve(approver="bob", requester="alice", action=_action(),
                    requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    assert ex.value.error_code == "APPROVAL_SOT_UNAVAILABLE"


def test_sot_authorized_allowed():
    # SoT 授权齐全 → 放行
    svc = ApprovalService(sot_provider=_FakeSoT({
        "enabled": True, "capabilities": ["action.approve", "action.confirm"],
    }))
    rec = svc.approve(approver="bob", requester="alice", action=_action(),
                      requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    assert rec.approver == "bob" and rec.requester == "alice"


def test_no_sot_provider_preserves_legacy_behavior():
    # 未配置 provider（In-memory MVP）→ 保持既有行为（放行）
    svc = ApprovalService()
    rec = svc.approve(approver="bob", requester="alice", action=_action(),
                      requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    assert rec.approver == "bob"


def test_self_approval_still_rejected_with_sot():
    # 即使 SoT 授权齐全，self-approval 仍拒绝（不因 SoT 放宽）
    svc = ApprovalService(sot_provider=_FakeSoT({
        "enabled": True, "capabilities": ["action.approve", "action.confirm"],
    }))
    with pytest.raises(Phase11Error) as ex:
        svc.approve(approver="alice", requester="alice", action=_action(),
                    requester_cluster=CLUSTER, approver_cluster=CLUSTER)
    assert ex.value.error_code == "SELF_APPROVAL"
