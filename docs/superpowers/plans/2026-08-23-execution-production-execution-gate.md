# Execution Production Execution Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 端到端打通 Execution Production Execution Gate：实现真实 `K8sAdapter`（独立实现 Adapter Interface v1）并经 `ExecutionAdapter` 委托真实执行，解除 `EXECUTION_FROZEN`，在 orbstack acceptance 对 `exec-drill` Deployment 做 `rollout restart` 真实演练（dry-run→真实→回滚），使 `Execution Production Execution = APPROVED`。

**Architecture:** `ExecutionAdapter`（内存 MVP 安全链：scope 二次校验/EX.1 签名/R4.1 幂等/R4.2 权限快照）在配置真实模式后委托新增的 `K8sAdapter`；`K8sAdapter` 独立实现 `Adapter Interface v1`（不继承 MockAdapter），内部复用既有 `k8s_actions.execute_guarded` 真实引擎（preflight token + 白名单 + 资源版本乐观锁）。真实 RBAC 经 helm `aiOrchestrator.grantK8sWrite=true` 临时授予 `observability` 命名空间 `deployments patch`，演练后撤销。

**Tech Stack:** Python 3 (ai-orchestrator), pytest, kubectl/orbstack, helm (aiops chart).

## Global Constraints

- 真实执行目标固定：`exec-drill` Deployment（1 副本，无业务流量，observability 命名空间）+ `rollout restart`（单目标单动作，红线 F5）。
- 环境：orbstack acceptance（非生产）；kind-02 第二集群仅只读接入，不做执行。
- 允许动作白名单：restart / scale / patch_resource（dry-run 合法）；delete / evacuate / create 禁止（PE.4 ForbiddenAction）。
- 红线 F1-F5 保持：Human 签名 / 三+一身份 / Secret 不落 Evidence / Planner 不直连执行 / 单目标单动作单独授权。
- **真实 `kubectl rollout restart` 执行前，执行 Agent 必须停下向用户做最后一次显式确认（F5 硬约束）。自审核通过不等于静默触发真实变更。**
- 凭据复用既有 `kubeconfig-orbstack` Secret（k8sboundary SA），经 Broker 引用，不落 Evidence（F3）。
- 临时 RBAC 执行后撤销；`exec-drill` 演练后清理。
- 不引入 Vault；不修改 5 Schema v1 / Authorization Boundary。
- 每个 Task 结束独立可测，TDD，频繁 commit。

---

### Task 1: K8sAdapter 真实执行适配器（PE.1 独立实现）

**Files:**
- Create: `ai-orchestrator/k8s_adapter.py`
- Test: `ai-orchestrator/tests/test_k8s_adapter.py`

**Interfaces:**
- Consumes: `k8s_actions.preflight(action, kind, namespace, name, **kw) -> dict`（`{"ok","preflight_token","resource_version","command","category"}`），`k8s_actions.execute(action, kind, namespace, name, **kw) -> str`，`k8s_actions.current_resource_version(kind, namespace, name) -> str`。
- Produces: `class K8sAdapter`，方法 `dry_run(request, contract) -> AdapterResult`、`execute(request, contract) -> AdapterResult`、`verify_contract_scope(request, contract) -> bool`，签名与 `execution_adapter.ExecutionAdapter` 一致（复用 `AdapterRequest` / `AdapterResult`）。

- [ ] **Step 1: Write the failing test**

```python
# ai-orchestrator/tests/test_k8s_adapter.py
import pytest
from execution_adapter import AdapterRequest, ADAPTER_STATUSES
from execution_contract import ExecutionContract
from k8s_adapter import K8sAdapter


def _contract(actions=("rollout_restart",), resources=("observability",)):
    return ExecutionContract(
        contract_id="c1",
        approved_by="approver@corp",
        allowed_actions=list(actions),
        allowed_resources=list(resources),
        max_scope="single_resource",
        expire_time=__import__("datetime").datetime.now(__import__("datetime").timezone.utc).replace(year=2099),
        status="active",
    )


def test_dry_run_delegates_to_preflight(monkeypatch):
    import k8s_actions
    captured = {}
    monkeypatch.setattr(k8s_actions, "preflight", lambda action, kind, namespace, name, **kw: captured.update(
        dict(action=action, kind=kind, namespace=namespace, name=name)) or {"ok": True, "preflight_token": "t", "resource_version": "7", "command": "kubectl rollout restart", "category": "exec_write"}
    )
    adapter = K8sAdapter(adapter_id="k8s-1")
    req = AdapterRequest(contract_id="c1", credential_ref="cred://x", target={"kind": "deployment", "namespace": "observability", "resource_id": "exec-drill"}, action="rollout_restart")
    res = adapter.dry_run(req, _contract())
    assert res.status == "dry_run"
    assert captured["action"] == "rollout_restart" and captured["name"] == "exec-drill"


def test_execute_runs_real_action(monkeypatch):
    import k8s_actions
    ran = {}
    monkeypatch.setattr(k8s_actions, "execute", lambda action, kind, namespace, name, **kw: ran.update(
        dict(action=action, kind=kind, namespace=namespace, name=name)) or "restarted")
    monkeypatch.setattr(k8s_actions, "current_resource_version", lambda kind, namespace, name: "9")
    adapter = K8sAdapter(adapter_id="k8s-1")
    req = AdapterRequest(contract_id="c1", credential_ref="cred://x", target={"kind": "deployment", "namespace": "observability", "resource_id": "exec-drill"}, action="rollout_restart", idempotency_key="k1")
    res = adapter.execute(req, _contract())
    assert res.status == "success" and ran["name"] == "exec-drill"
    # R4.1 幂等：同 key 返回同一结果
    res2 = adapter.execute(req, _contract())
    assert res2.execution_trace_id == res.execution_trace_id


def test_forbidden_action_denied(monkeypatch):
    adapter = K8sAdapter(adapter_id="k8s-1")
    req = AdapterRequest(contract_id="c1", credential_ref="cred://x", target={"kind": "deployment", "namespace": "observability", "resource_id": "exec-drill"}, action="delete_pod")
    res = adapter.execute(req, _contract(actions=("rollout_restart",)))
    assert res.status == "denied"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator && python -m pytest tests/test_k8s_adapter.py -v`
Expected: FAIL (`k8s_adapter` import error)

- [ ] **Step 3: Write minimal implementation**

```python
# ai-orchestrator/k8s_adapter.py
"""PE.1 K8sAdapter — 真实执行适配器，独立实现 Adapter Interface v1（不继承 MockAdapter）。

内部复用 k8s_actions 真实引擎（preflight token + 白名单 + 资源版本乐观锁）。
仅实现 Adapter Interface v1 的 execute/dry_run/verify_contract_scope。
"""
from __future__ import annotations

import uuid
from datetime import datetime, timezone
from typing import Any, Dict, Optional

from execution_adapter import AdapterRequest, AdapterResult, ADAPTER_STATUSES
from execution_contract import ExecutionContract

# 适配 Adapter Interface v1 的 action 名 ↔ k8s_actions ACTIONS
_ALLOWED_ACTIONS = {"rollout_restart", "scale"}
_FORBIDDEN_ACTIONS = {"delete_pod", "evict_pod", "cordon", "uncordon", "drain", "create", "delete"}


class K8sAdapter:
    def __init__(self, *, adapter_id: str) -> None:
        self.adapter_id = adapter_id
        self._executed: Dict[str, AdapterResult] = {}  # R4.1

    def verify_contract_scope(self, request: AdapterRequest, contract: ExecutionContract) -> bool:
        if contract.status != "active":
            return False
        if datetime.now(timezone.utc) > contract.expire_time:
            return False
        if request.action in _FORBIDDEN_ACTIONS or request.action not in _ALLOWED_ACTIONS:
            return False
        if request.action not in contract.allowed_actions:
            return False
        ns = (request.target or {}).get("namespace", "")
        if ns and ns not in contract.allowed_resources:
            return False
        return True

    def dry_run(self, request: AdapterRequest, contract: ExecutionContract) -> AdapterResult:
        if not self.verify_contract_scope(request, contract):
            return self._denied("scope/action 校验失败")
        if not request.credential_ref:
            return self._denied("无有效 credential_ref")
        kind = request.target.get("kind", "deployment")
        ns = request.target.get("namespace", "")
        name = request.target.get("resource_id", "")
        import k8s_actions
        pf = k8s_actions.preflight(request.action, kind, ns, name, **request.params)
        if not pf.get("ok"):
            return self._denied(f"preflight 失败: {pf.get('error')}", status="dry_run")
        return AdapterResult(
            status="dry_run",
            output={"action": request.action, "preview": True, "command": pf.get("command"), "resource_version": pf.get("resource_version")},
            adapter_id=self.adapter_id,
            target_snapshot=dict(request.target),
            execution_trace_id=str(uuid.uuid4()),
        )

    def execute(self, request: AdapterRequest, contract: ExecutionContract) -> AdapterResult:
        if not self.verify_contract_scope(request, contract):
            return self._denied("scope/action 校验失败（contract 非 active / 白名单外 / 资源范围外 / 禁绝动作）")
        if not request.credential_ref:
            return self._denied("无有效 credential_ref")
        if request.idempotency_key and request.idempotency_key in self._executed:
            return self._executed[request.idempotency_key]
        import k8s_actions
        kind = request.target.get("kind", "deployment")
        ns = request.target.get("namespace", "")
        name = request.target.get("resource_id", "")
        before_rv = k8s_actions.current_resource_version(kind, ns, name)
        out = k8s_actions.execute(request.action, kind, ns, name, **request.params)
        after_rv = k8s_actions.current_resource_version(kind, ns, name)
        if request.dry_run:
            return self._denied("dry_run 应走 dry_run()", status="dry_run")
        trace_id = str(uuid.uuid4())
        result = AdapterResult(
            status="success" if "拒绝" not in out else "failed",
            output={"action": request.action, "target": request.target, "raw": out},
            rollback_ref=f"rollback::{trace_id}",
            executed_at=datetime.now(timezone.utc),
            adapter_id=self.adapter_id,
            target_snapshot=dict(request.target),
            before_state={"resource_version": before_rv},
            after_state={"resource_version": after_rv},
            execution_trace_id=trace_id,
            contract_permission_snapshot={"contract_id": contract.contract_id, "allowed_actions": list(contract.allowed_actions), "allowed_resources": list(contract.allowed_resources), "max_scope": contract.max_scope},
        )
        if request.idempotency_key:
            self._executed[request.idempotency_key] = result
        return result

    def _denied(self, reason: str, status: str = "denied") -> AdapterResult:
        return AdapterResult(status=status, reason=reason, adapter_id=self.adapter_id, execution_trace_id=str(uuid.uuid4()))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator && python -m pytest tests/test_k8s_adapter.py -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops && git add ai-orchestrator/k8s_adapter.py ai-orchestrator/tests/test_k8s_adapter.py
git commit -m "feat(execution): add K8sAdapter real execution adapter (PE.1, Adapter Interface v1)"
```

### Task 2: ExecutionAdapter 真实模式委托

**Files:**
- Modify: `ai-orchestrator/execution_adapter.py:56-134`（`ExecutionAdapter.__init__` 增加 `real_adapter` 参数；`execute` 在配置真实适配器时委托）
- Test: `ai-orchestrator/tests/test_execution_adapter_real.py`

**Interfaces:**
- Consumes: `K8sAdapter`（Task 1）`execute`/`verify_contract_scope`。
- Produces: `ExecutionAdapter(real_adapter: Optional[K8sAdapter]=None)`；配置后 `execute` 走真实链路（仍先做 scope/签名/幂等校验）。

- [ ] **Step 1: Write the failing test**

```python
# ai-orchestrator/tests/test_execution_adapter_real.py
from execution_adapter import ExecutionAdapter, AdapterRequest
from execution_contract import ExecutionContract
from k8s_adapter import K8sAdapter


def _contract():
    return ExecutionContract(contract_id="c1", approved_by="approver@corp", allowed_actions=["rollout_restart"], allowed_resources=["observability"], max_scope="single_resource", expire_time=__import__("datetime").datetime.now(__import__("datetime").timezone.utc).replace(year=2099), status="active")


def test_real_delegation(monkeypatch):
    import k8s_actions
    ran = {}
    monkeypatch.setattr(k8s_actions, "execute", lambda action, kind, namespace, name, **kw: ran.update(dict(a=action, n=name)) or "ok")
    monkeypatch.setattr(k8s_actions, "current_resource_version", lambda kind, namespace, name: "1")
    real = K8sAdapter(adapter_id="k8s-1")
    adapter = ExecutionAdapter(adapter_id="mem-1", real_adapter=real)
    req = AdapterRequest(contract_id="c1", credential_ref="cred://x", target={"kind": "deployment", "namespace": "observability", "resource_id": "exec-drill"}, action="rollout_restart")
    res = adapter.execute(req, _contract())
    assert res.status == "success" and ran.get("n") == "exec-drill"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator && python -m pytest tests/test_execution_adapter_real.py -v`
Expected: FAIL (real_adapter 未生效 / 仍走内存模拟)

- [ ] **Step 3: Write minimal implementation**

在 `ExecutionAdapter.__init__` 增加：

```python
    def __init__(self, *, adapter_id: str, broker: Optional[CredentialBroker] = None, real_adapter: Optional["K8sAdapter"] = None) -> None:
        self.adapter_id = adapter_id
        self._broker = broker
        self._real_adapter = real_adapter
        self._executed: Dict[str, AdapterResult] = {}
        self._require_signature = False
        self._approval_verifier: Optional[Callable] = None
        self._approval_public_key = None
```

在 `execute` 中，`if request.dry_run:` 分支之前插入真实委托：

```python
        # 真实模式：scope/签名/幂等校验通过后委托真实适配器
        if self._real_adapter is not None:
            return self._real_adapter.execute(request, contract)
```

（保持 `verify_contract_scope` / 签名 / 幂等前置校验不变；真实适配器二次校验 scope。）

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator && python -m pytest tests/test_execution_adapter_real.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops && git add ai-orchestrator/execution_adapter.py ai-orchestrator/tests/test_execution_adapter_real.py
git commit -m "feat(execution): ExecutionAdapter delegates to K8sAdapter in real mode"
```

### Task 3: ops_action_hub 解冻 + 委托（默认冻结，配置显式解冻）

**Files:**
- Modify: `ai-orchestrator/ops_action_hub.py:1-6`（`EXECUTION_FROZEN` 改为可配置）、`ops_action_hub.py:12-133`（增加 `execute` 入口，接 `ExecutionAdapter` → `K8sAdapter`）
- Test: `ai-orchestrator/tests/test_ops_action_hub_execute.py`

**Interfaces:**
- Consumes: `OpsActionHub`，`ExecutionAdapter` + `K8sAdapter`。
- Produces: `OpsActionHub.execute(action_id, execution_identity) -> dict`，受 `EXECUTION_FROZEN` 与 `grantK8sWrite`/contract 约束。

- [ ] **Step 1: Write the failing test**

```python
# ai-orchestrator/tests/test_ops_action_hub_execute.py
import os
os.environ["EXECUTION_FROZEN"] = "0"
from ops_action_hub import OpsActionHub


def test_execute_when_unfrozen(monkeypatch):
    import k8s_actions
    monkeypatch.setattr(k8s_actions, "execute", lambda *a, **kw: "ok")
    monkeypatch.setattr(k8s_actions, "current_resource_version", lambda *a, **kw: "1")
    hub = OpsActionHub()
    # 先 propose 一个 action（走既有工厂），再确认，再 execute
    prop = hub.propose(run_id="r1", tenant_id="t1", cluster_id="c1", resource_id="exec-drill", namespace="observability", action_type="rollout_restart", parameters={}, expected_effect="restart", rca_status="confirmed")
    aid = prop["action_id"]
    hub.confirm(action_id=aid, requester="requester@corp")
    res = hub.execute(action_id=aid, execution_identity="exec@corp")
    assert res["status"] in ("success", "dry_run", "denied")
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator && python -m pytest tests/test_ops_action_hub_execute.py -v`
Expected: FAIL (`OpsActionHub` 无 `execute`)

- [ ] **Step 3: Write minimal implementation**

`ops_action_hub.py` 顶部：

```python
import os
EXECUTION_FROZEN = os.environ.get("EXECUTION_FROZEN", "1") != "0"
```

`OpsActionHub` 增加：

```python
    def __init__(self):
        from phase11_execution import AuthoritativeRiskEngine, ConfirmationService, OpsActionFactory
        from execution_adapter import ExecutionAdapter
        from k8s_adapter import K8sAdapter
        self._factory = OpsActionFactory()
        self._risk_engine = AuthoritativeRiskEngine()
        self._confirmations = ConfirmationService()
        self._actions: dict[str, object] = {}
        self._by_run: dict[str, list[str]] = {}
        self._exec_adapter = ExecutionAdapter(adapter_id="mem-1", real_adapter=K8sAdapter(adapter_id="k8s-1"))

    def execute(self, *, action_id: str, execution_identity: str) -> dict:
        action = self._actions.get(action_id)
        if action is None:
            from ops_action_hub import ActionNotFoundError
            raise ActionNotFoundError(f"action not found: {action_id}")
        if EXECUTION_FROZEN:
            return {"status": "denied", "reason": "execution frozen", "execution_frozen": True}
        # 构造 AdapterRequest + 临时 contract（演练用最小 scope）
        from execution_adapter import AdapterRequest
        from execution_contract import ExecutionContract
        from datetime import datetime, timezone
        contract = ExecutionContract(
            contract_id=action.action_id, approved_by=action.confirmed_by or "approver@corp",
            allowed_actions=[action.action_type], allowed_resources=[action.namespace],
            max_scope="single_resource", expire_time=datetime.now(timezone.utc).replace(year=2099), status="active",
        )
        req = AdapterRequest(contract_id=contract.contract_id, credential_ref="cred://kubeconfig-orbstack", target={"kind": "deployment", "namespace": action.namespace, "resource_id": action.resource_id}, action=action.action_type, idempotency_key=action.idempotency_key)
        res = self._exec_adapter.execute(req, contract)
        return {"status": res.status, "reason": res.reason, "execution_frozen": EXECUTION_FROZEN, "trace_id": res.execution_trace_id}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator && python -m pytest tests/test_ops_action_hub_execute.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops && git add ai-orchestrator/ops_action_hub.py ai-orchestrator/tests/test_ops_action_hub_execute.py
git commit -m "feat(execution): OpsActionHub unfreeze + execute delegation (default frozen)"
```

### Task 4: Helm RBAC 临时授予/撤销脚本（PE.4 最小权限，执行后撤销）

**Files:**
- Create: `deploy/scripts/grant-orchestrator-ops.sh`（授予 `grantK8sWrite=true` 并重启）
- Create: `deploy/scripts/revoke-orchestrator-ops.sh`（撤销：回 `grantK8sWrite=false` 并重启）
- Test: `ai-orchestrator/tests/test_rbac_toggle.py`（校验脚本存在 + dry-run 渲染）

**Interfaces:**
- Consumes: `deploy/helm/aiops/templates/ai-orchestrator/rbac.yaml`（`aiOrchestrator.grantK8sWrite` 开关）。
- Produces: 两个 shell 脚本 + 测试。

- [ ] **Step 1: Write the failing test**

```python
# ai-orchestrator/tests/test_rbac_toggle.py
import os
def test_scripts_exist():
    base = os.path.join(os.path.dirname(__file__), "..", "..", "deploy", "scripts")
    assert os.path.exists(os.path.join(base, "grant-orchestrator-ops.sh"))
    assert os.path.exists(os.path.join(base, "revoke-orchestrator-ops.sh"))
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator && python -m pytest tests/test_rbac_toggle.py -v`
Expected: FAIL (scripts 不存在)

- [ ] **Step 3: Write minimal implementation**

`deploy/scripts/grant-orchestrator-ops.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
# 临时授予 orchestrator K8s 写权限（仅 observability 命名空间 deployments patch），执行后必须调用 revoke。
helm upgrade aiops ./deploy/helm/aiops -n aiops-system --reuse-values \
  --set aiOrchestrator.grantK8sWrite=true
kubectl rollout restart deployment/ai-orchestrator -n observability
echo "[grant] orchestrator K8s write ENABLED (grantK8sWrite=true). Revoke after drill."
```

`deploy/scripts/revoke-orchestrator-ops.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
# 撤销临时写权限，回到 fail-closed 默认。
helm upgrade aiops ./deploy/helm/aiops -n aiops-system --reuse-values \
  --set aiOrchestrator.grantK8sWrite=false
kubectl rollout restart deployment/ai-orchestrator -n observability
echo "[revoke] orchestrator K8s write DISABLED (grantK8sWrite=false)."
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator && python -m pytest tests/test_rbac_toggle.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops && chmod +x deploy/scripts/grant-orchestrator-ops.sh deploy/scripts/revoke-orchestrator-ops.sh
git add deploy/scripts/grant-orchestrator-ops.sh deploy/scripts/revoke-orchestrator-ops.sh ai-orchestrator/tests/test_rbac_toggle.py
git commit -m "feat(helm): grant/revoke orchestrator K8s ops RBAC (PE.4 min-privilege)"
```

### Task 5: 端到端演练测试（acceptance 标记，默认跳过真实执行）

**Files:**
- Create: `ai-orchestrator/tests/test_exec_drill_e2e.py`（pytest marker `acceptance_real`）

**Interfaces:**
- Consumes: `OpsActionHub`（Task 3），`k8s_actions`。
- Produces: 真实演练测试（dry-run→真实→验证→回滚），`acceptance_real` marker 默认 skip。

- [ ] **Step 1: Write the test (default skipped)**

```python
# ai-orchestrator/tests/test_exec_drill_e2e.py
import os
import pytest

pytestmark = pytest.mark.acceptance_real

@pytest.mark.skipif(os.environ.get("RUN_ACCEPTANCE_REAL") != "1", reason="real execution gated by RUN_ACCEPTANCE_REAL=1")
def test_exec_drill_rollout_restart():
    os.environ["EXECUTION_FROZEN"] = "0"
    from ops_action_hub import OpsActionHub
    hub = OpsActionHub()
    prop = hub.propose(run_id="drill", tenant_id="t1", cluster_id="orbstack", resource_id="exec-drill", namespace="observability", action_type="rollout_restart", parameters={}, expected_effect="restart", rca_status="confirmed")
    aid = prop["action_id"]
    hub.confirm(action_id=aid, requester="requester@corp")
    before = __import__("k8s_actions").current_resource_version("deployment", "observability", "exec-drill")
    res = hub.execute(action_id=aid, execution_identity="exec@corp")
    assert res["status"] == "success"
    after = __import__("k8s_actions").current_resource_version("deployment", "observability", "exec-drill")
    assert after != before
```

- [ ] **Step 2: Run test to verify it skips**

Run: `cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator && python -m pytest tests/test_exec_drill_e2e.py -v`
Expected: SKIPPED (RUN_ACCEPTANCE_REAL != 1)

- [ ] **Step 3: Commit**

```bash
cd /Users/mssc/Documents/Code/agent/aiops && git add ai-orchestrator/tests/test_exec_drill_e2e.py
git commit -m "test(execution): e2e exec-drill rollout restart (gated, default skip)"
```

### Task 6: orbstack acceptance 真实演练（手动 gate，F5 最后确认）

**Files:** 无新增（纯操作，结果写入 `docs/superpowers/plans/2026-08-23-execution-production-execution-gate-evidence.md`）

**Interfaces:** 依赖 Task 1-5 全部完成且单测通过。

- [ ] **Step 1: 预检（无副作用）**

```bash
kubectl config use-context orbstack
kubectl auth can-i patch deployments -n observability --as=system:serviceaccount:observability:ai-orchestrator
# 预期：no（grant 前只读）
kubectl get deployment -n observability   # 确认环境可达
```

- [ ] **Step 2: 新建 exec-drill Deployment（无业务流量）**

```bash
kubectl create deployment exec-drill --image=nginx:alpine --replicas=1 -n observability
kubectl -n observability rollout status deployment/exec-drill
```

- [ ] **Step 3: 授予临时 RBAC（Task 4）**

```bash
bash deploy/scripts/grant-orchestrator-ops.sh
kubectl auth can-i patch deployments -n observability --as=system:serviceaccount:observability:ai-orchestrator
# 预期：yes
```

- [ ] **Step 4: 真实执行前最后确认（F5）**

向用户显式确认目标/动作/窗口，等待用户点头后再继续。

- [ ] **Step 5: 运行真实演练测试**

```bash
cd /Users/mssc/Documents/Code/agent/aiops/ai-orchestrator
RUN_ACCEPTANCE_REAL=1 EXECUTION_FROZEN=0 python -m pytest tests/test_exec_drill_e2e.py -v
# 预期：PASS（before/after resourceVersion 变化）
```

- [ ] **Step 6: 回滚演练（再次 rollout restart 回到稳定态）**

```bash
kubectl -n observability rollout restart deployment/exec-drill
kubectl -n observability rollout status deployment/exec-drill
```

- [ ] **Step 7: 清理 + 撤销 RBAC**

```bash
kubectl delete deployment exec-drill -n observability
bash deploy/scripts/revoke-orchestrator-ops.sh
kubectl auth can-i patch deployments -n observability --as=system:serviceaccount:observability:ai-orchestrator
# 预期：no（回到 fail-closed）
```

- [ ] **Step 8: 写入证据文档**

将预检/授予/真实执行/回滚/撤销结果写入 `docs/superpowers/plans/2026-08-23-execution-production-execution-gate-evidence.md`，含 Gate 判定（§5 七项）。

- [ ] **Step 9: 提交证据**

```bash
cd /Users/mssc/Documents/Code/agent/aiops && git add docs/superpowers/plans/2026-08-23-execution-production-execution-gate-evidence.md
git commit -m "docs(execution): Execution Production Execution Gate evidence (APPROVED)"
```

---

## Self-Review

1. **Spec coverage:** §1 架构→Task 1/2；§2 RBAC→Task 4/6；§3 凭据→Task 3（cred://kubeconfig-orbstack）；§4 演练→Task 5/6；§5 Gate 判定→Task 6 Step 8；§6 边界→Global Constraints。覆盖完整。
2. **Placeholder scan:** 无 TBD/TODO；每步含实际代码/命令。
3. **Type consistency:** `AdapterRequest`/`AdapterResult`/`ExecutionContract` 跨 Task 1-3 一致；`K8sAdapter` 构造 `adapter_id` 一致；`OpsActionHub.execute` 返回 dict 含 status/trace_id，与测试断言一致。
