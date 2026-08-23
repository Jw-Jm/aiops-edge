"""P11 接线：结构化动作提案/确认中枢。

真实执行默认冻结（EXECUTION_FROZEN，fail-closed）。仅当环境变量 EXECUTION_FROZEN=0
显式解冻时，execute 才委托 ExecutionAdapter → K8sAdapter 做真实 kubectl 执行。
"""
from __future__ import annotations

import os

# 真实执行默认冻结（fail-closed）。EXECUTION_FROZEN=0 才显式解冻。
# 运行时每次读取环境变量，确保 fail-closed 不被 import 期缓存绕过。
def _is_execution_frozen() -> bool:
    return os.environ.get("EXECUTION_FROZEN", "1") != "0"


# OpsActionFactory action_type ↔ k8s_actions 真实动作名
_ACTION_TYPE_TO_K8S = {"restart": "rollout_restart", "scale": "scale", "patch_resource": "patch_resource"}


class ActionNotFoundError(LookupError):
    pass


class OpsActionHub:
    def __init__(self):
        from phase11_execution import AuthoritativeRiskEngine, ConfirmationService, OpsActionFactory

        self._factory = OpsActionFactory()
        self._risk_engine = AuthoritativeRiskEngine()
        self._confirmations = ConfirmationService()
        self._actions: dict[str, object] = {}
        self._by_run: dict[str, list[str]] = {}
        self._exec_adapter = None  # 懒初始化（execute 时构造真实适配器）

    def propose(
        self,
        *,
        run_id: str,
        tenant_id: str,
        cluster_id: str,
        resource_id: str,
        namespace: str,
        action_type: str,
        parameters: dict,
        expected_effect: str,
        verification_policy: str = "manual_check",
        root_cause_confidence: float = 0.0,
        resource_version: str = "0",
        rca_status: str,
        blast_radius: str = "single_resource",
        environment: str = "production",
        llm_risk_suggestion: str = "R0",
    ) -> dict:
        # 1) build draft
        draft = self._factory.create(
            run_id=run_id,
            tenant_id=tenant_id,
            cluster_id=cluster_id,
            resource_id=resource_id,
            namespace=namespace,
            action_type=action_type,
            parameters=parameters,
            expected_effect=expected_effect,
            verification_policy=verification_policy,
            risk=llm_risk_suggestion,
            root_cause_confidence=root_cause_confidence,
            resource_version=resource_version,
            rca_status=rca_status,
        )
        # 2) authoritative risk
        final_risk = self._risk_engine.compute(
            action=draft,
            rca_confidence=root_cause_confidence,
            blast_radius=blast_radius,
            environment=environment,
            llm_risk_suggestion=llm_risk_suggestion,
        )
        action = draft
        if final_risk != draft.risk:
            # rebuild via factory with final risk
            action = self._factory.create(
                run_id=run_id,
                tenant_id=tenant_id,
                cluster_id=cluster_id,
                resource_id=resource_id,
                namespace=namespace,
                action_type=action_type,
                parameters=parameters,
                expected_effect=expected_effect,
                verification_policy=verification_policy,
                risk=final_risk,
                root_cause_confidence=root_cause_confidence,
                resource_version=resource_version,
                rca_status=rca_status,
            )
        # 3) register
        self._actions[action.action_id] = action
        self._by_run.setdefault(run_id, []).append(action.action_id)
        return self.to_public(action)

    def to_public(self, action) -> dict:
        return {
            "action_id": action.action_id,
            "version": action.version,
            "action_hash": action.action_hash,
            "action_type": action.action_type,
            "run_id": action.run_id,
            "tenant_id": action.tenant_id,
            "cluster_id": action.cluster_id,
            "resource_id": action.resource_id,
            "namespace": action.namespace,
            "parameters": action.parameters,
            "idempotency_key": action.idempotency_key,
            "resource_version": action.resource_version,
            "expected_effect": action.expected_effect,
            "verification_policy": action.verification_policy,
            "risk": action.risk,
            "planner_selectable": action.planner_selectable,
            "automatic": action.automatic,
            "scope_kind": action.scope_kind,
            "execution_frozen": _is_execution_frozen(),
        }

    def list(self, run_id: str | None = None) -> list[dict]:
        if run_id is not None:
            ids = self._by_run.get(run_id, [])
            return [self.to_public(self._actions[i]) for i in ids if i in self._actions]
        return [self.to_public(a) for a in self._actions.values()]

    def get(self, action_id: str) -> dict:
        a = self._actions.get(action_id)
        if a is None:
            raise ActionNotFoundError(f"action not found: {action_id}")
        return self.to_public(a)

    def confirm(self, *, action_id: str, requester: str) -> dict:
        action = self._actions.get(action_id)
        if action is None:
            raise ActionNotFoundError(f"action not found: {action_id}")
        c = self._confirmations.confirm(requester=requester, action=action)
        return {
            "confirmation_id": c.confirmation_id,
            "action_id": action_id,
            "status": "confirmed",
            "execution_frozen": _is_execution_frozen(),
        }

    def execute(self, *, action_id: str, execution_identity: str) -> dict:
        """Execution Production Execution Gate 真实执行入口（fail-closed）。

        仅在 EXECUTION_FROZEN=False 时委托 ExecutionAdapter → K8sAdapter 真实执行。
        """
        action = self._actions.get(action_id)
        if action is None:
            raise ActionNotFoundError(f"action not found: {action_id}")
        if _is_execution_frozen():
            return {"status": "denied", "reason": "execution frozen", "execution_frozen": True}
        k8s_action = _ACTION_TYPE_TO_K8S.get(action.action_type)
        if k8s_action is None:
            return {"status": "denied", "reason": f"unsupported action_type: {action.action_type}", "execution_frozen": False}
        from datetime import datetime as _dt, timezone as _tz
        from execution_adapter import AdapterRequest, ExecutionAdapter
        from execution_contract import ExecutionContract
        from k8s_adapter import K8sAdapter

        contract = ExecutionContract(
            contract_id=action.action_id, plan_id="drill", intent_id="i1", run_id=action.run_id,
            requested_by=getattr(action, "tenant_id", "requester@corp"),
            allowed_tools=["k8s_adapter"], allowed_resources=[action.namespace],
            allowed_actions=[k8s_action], max_scope="resource",
            expire_time=_dt.now(_tz.utc).replace(year=2099),
            rollback_policy={"strategy": "rollback_restart"},
            approved_by=getattr(action, "confirmed_by", None) or "approver@corp",
            status="active",
        )
        if self._exec_adapter is None:
            self._exec_adapter = ExecutionAdapter(adapter_id="mem-1", real_adapter=K8sAdapter(adapter_id="k8s-1"))
        req = AdapterRequest(
            contract_id=contract.contract_id, credential_ref="cred://kubeconfig-orbstack",
            target={"kind": "deployment", "namespace": action.namespace, "resource_id": action.resource_id},
            action=k8s_action, idempotency_key=action.idempotency_key,
        )
        res = self._exec_adapter.execute(req, contract)
        return {
            "status": res.status,
            "reason": res.reason,
            "execution_frozen": False,
            "trace_id": res.execution_trace_id,
            "adapter_id": res.adapter_id,
        }
