"""P0-4 真实执行器与 Runtime 组合测试（审计阻断项 B0-04）。

此前 agent_runtime 只调用 tool_executor(params)，而 RealToolExecutor 强制要求
tool_id/tenant_id/cluster_id/context 四个关键字参数 → 立即 TypeError。
修复后统一执行接口，本测试验证真实 executor 可无缝接入 Runtime。
"""
import pytest


TENANT = "7ed01afc-cc79-4ecd-8767-a2befa6168ad"
CLUSTER = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"


def _framework():
    from agent_runtime import AgentRuntimeFramework
    from evidence_hub import EvidenceHub
    from tool_registry import ToolRegistry, init_default_tool_registry

    ToolRegistry._tools.clear()
    init_default_tool_registry()
    return AgentRuntimeFramework(registry=ToolRegistry, evidence_hub=EvidenceHub())


def test_real_executor_composes_with_runtime():
    """RealToolExecutor 的 __call__ 必须接受统一执行接口（无 TypeError）。

    验证方式：mock client，验证 RealToolExecutor.__call__ 签名与
    agent_runtime 传参契约一致（params + 4 keyword args）。
    """
    from agent_runtime_integration import RealToolExecutor
    from tool_registry import ToolRegistry, init_default_tool_registry

    ToolRegistry._tools.clear()
    init_default_tool_registry()
    captured = {}

    class FakeClient:
        def query(self, **kw):
            captured.update(kw)
            from internal_query_client import QueryResult
            return QueryResult(200, {"logs": [{"ts": 1}], "total": 1})

    executor = RealToolExecutor(client=FakeClient())
    result = executor(
        {"service": "checkout"},
        tool_id="query_logs.v1",
        tenant_id=TENANT,
        cluster_id=CLUSTER,
        context={"run_id": "run-1"},
    )
    # 不再 TypeError：executor 收到完整上下文并转发给 client
    assert result is not None
    assert captured["tool_id"] == "query_logs.v1"


def test_agent_runtime_passes_full_context_to_executor():
    """execute_step 必须把 tool_id/tenant_id/cluster_id/context 传给 executor。"""
    fw = _framework()
    captured = {}

    def spy_executor(params, **kw):
        captured.update(kw)
        from internal_query_client import QueryResult
        return QueryResult(200, {"logs": [{"ts": 1}], "total": 1})

    fw.execute_step(
        tool_id="query_metrics.v1", params={"service": "checkout"},
        tenant_id=TENANT, cluster_id=CLUSTER,
        context={"run_id": "run-1", "request_id": "req-1", "query_id": "q1",
                 "time_range": "2026-08-23T08:00:00Z"},
        evidence_type="metric_anomaly",
        tool_executor=spy_executor,
    )
    assert captured["tool_id"] == "query_metrics.v1"
    assert captured["tenant_id"] == TENANT
    assert captured["cluster_id"] == CLUSTER
    assert captured["context"] == {"run_id": "run-1", "request_id": "req-1", "query_id": "q1",
                                   "time_range": "2026-08-23T08:00:00Z"}


def test_executor_without_context_breaks():
    """旧式 executor（只接受 params）应因接口不匹配而暴露（不再静默兼容）。"""
    fw = _framework()

    def legacy_executor(params):
        from internal_query_client import QueryResult
        return QueryResult(200, {"logs": [{"ts": 1}], "total": 1})

    with pytest.raises(TypeError):
        fw.execute_step(
            tool_id="query_logs.v1", params={}, tenant_id=TENANT, cluster_id=CLUSTER,
            context={}, evidence_type="log_pattern", tool_executor=legacy_executor,
        )
