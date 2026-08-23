# ai-orchestrator/tests/test_exec_drill_e2e.py
"""Execution Production Execution Gate 端到端演练（真实 kubectl，acceptance 环境）。

默认跳过：仅当 RUN_ACCEPTANCE_REAL=1 且 EXECUTION_FROZEN=0 时运行。
演练目标：exec-drill Deployment（observability 命名空间）+ rollout restart。
"""
import os
import pytest

pytestmark = pytest.mark.acceptance_real

k8s_actions = __import__("k8s_actions")


@pytest.mark.skipif(os.environ.get("RUN_ACCEPTANCE_REAL") != "1", reason="real execution gated by RUN_ACCEPTANCE_REAL=1")
def test_exec_drill_rollout_restart():
    os.environ["EXECUTION_FROZEN"] = "0"
    from ops_action_hub import OpsActionHub
    hub = OpsActionHub()
    prop = hub.propose(
        run_id="drill", tenant_id="t1", cluster_id="orbstack", resource_id="exec-drill",
        namespace="observability", action_type="restart", parameters={},
        expected_effect="restart", rca_status="confirmed",
    )
    aid = prop["action_id"]
    hub.confirm(action_id=aid, requester="requester@corp")
    before = k8s_actions.current_resource_version("deployment", "observability", "exec-drill")
    res = hub.execute(action_id=aid, execution_identity="exec@corp")
    assert res["status"] == "success", res
    after = k8s_actions.current_resource_version("deployment", "observability", "exec-drill")
    assert after != before, f"resourceVersion 未变化: before={before} after={after}"
