"""C3: chat 工具注册 execute_k8s_action / describe_k8s_resource

- describe_k8s_resource: Class=safe, 不经审批直接可用
- execute_k8s_action: Class=dangerous + requires_approval, 未审批被 execution_gate 拒绝
"""
import pytest

import k8s_actions
from execution_gate import check_tool_executable
from k8s_actions import describe_k8s_resource, execute_k8s_action, register_k8s_tools
from skill_registry import ToolRegistry


@pytest.fixture(autouse=True)
def _register():
    for name in ("execute_k8s_action", "describe_k8s_resource"):
        ToolRegistry._tools.pop(name, None)
    register_k8s_tools(ToolRegistry)
    yield
    for name in ("execute_k8s_action", "describe_k8s_resource"):
        ToolRegistry._tools.pop(name, None)


def test_describe_registered_safe_and_directly_executable():
    td = ToolRegistry.get("describe_k8s_resource")
    assert td is not None
    assert td.cls == "safe" and td.requires_approval is False
    ok, reason = check_tool_executable(td, approved=False)
    assert ok, reason


def test_execute_registered_dangerous_requires_approval():
    td = ToolRegistry.get("execute_k8s_action")
    assert td is not None
    assert td.cls == "dangerous" and td.requires_approval is True
    ok, reason = check_tool_executable(td, approved=False)
    assert not ok, "未审批时 dangerous 工具应被 execution_gate 拦截"
    assert "dangerous" in reason.lower() or "审批" in reason
    ok, _ = check_tool_executable(td, approved=True)
    assert ok


def test_describe_runs_kubectl_describe(monkeypatch):
    calls = []
    monkeypatch.setattr(k8s_actions, "_run_cmd",
                        lambda cmd, timeout=30: (calls.append(cmd) or "DESC-OUT"))
    out = describe_k8s_resource(kind="pod", name="p1", namespace="ns1")
    assert calls[0] == "kubectl describe pod/p1 -n ns1"
    assert "DESC-OUT" in out


def test_describe_without_namespace(monkeypatch):
    calls = []
    monkeypatch.setattr(k8s_actions, "_run_cmd",
                        lambda cmd, timeout=30: (calls.append(cmd) or "NODE-DESC"))
    out = describe_k8s_resource(kind="node", name="n1", namespace="")
    assert calls[0] == "kubectl describe node/n1"
    assert "NODE-DESC" in out


def test_execute_action_runs_command(monkeypatch):
    calls = []
    monkeypatch.setattr(k8s_actions, "_run_cmd",
                        lambda cmd, timeout=30: (calls.append(cmd) or "SCALE-OUT"))
    out = execute_k8s_action(action="scale", kind="deployment", namespace="ns1",
                             name="svc", replicas=2)
    assert calls[0] == "kubectl scale deployment/svc --replicas=2 -n ns1"
    assert "SCALE-OUT" in out


def test_execute_action_rejects_non_whitelisted_combination(monkeypatch):
    """工具层对白名单外动作/资源类型组合直接拒绝, 不执行子进程。"""
    called = []

    def _no_run(cmd, timeout=30):
        called.append(cmd)
        return "SHOULD-NOT-RUN"

    monkeypatch.setattr(k8s_actions, "_run_cmd", _no_run)
    out = execute_k8s_action(action="delete_pod", kind="deployment", namespace="ns1",
                             name="svc")  # delete_pod 仅支持 pod
    assert called == [], "非法 kind 不应触发子进程"
    assert "安全策略拒绝" in out or "不支持" in out
