"""P1-S1: cluster_checks 结构化 kubectl 执行路径安全测试。

覆盖审核报告 §5.3 的 15 项强制安全测试 + 补充回归：
- 结构化 ClusterCheck 模型（kind/namespace/pod），LLM 不再输出 shell command
- kubectl 一律 argv 执行（shell=False）
- 管道/后处理全部在 Python 内实现（tail/head/grep/tr/sort/wc），awk 删除
- 参数校验 fail-closed：路径穿越/flag 注入/任意 shell 字符串全部拒绝
- 审计记录含 check_kind/namespace/pod/exit_code/duration/truncated，不含 Secret
"""
import subprocess
from pathlib import Path
from unittest.mock import patch

import pytest

import cluster_checks
from cluster_checks import (
    AUDIT_LOG,
    DEFAULT_NAMESPACE,
    OUTPUT_LIMIT_CHARS,
    TIMEOUT_CAP,
    ClusterCheck,
    InvalidClusterCheck,
    build_argv,
    check_from_hypothesis,
    parse_cluster_check,
    run_cluster_check,
)

ORCH_DIR = Path(__file__).resolve().parent.parent


# ── 1. 任何 check 都不得产生 shell=True ──────────────────────────────

def test_no_shell_true_in_rca_and_cluster_checks():
    """关闭条件: git grep -n "shell=True" -- rca.py cluster_checks.py 必须零命中。"""
    for name in ("rca.py", "cluster_checks.py"):
        src = (ORCH_DIR / name).read_text(encoding="utf-8")
        assert "shell=True" not in src, f"{name} 仍存在 shell=True（P1-S1 关闭条件失败）"


def test_no_shell_invocation_in_cluster_checks():
    """cluster_checks 源码不得出现 shell 语义（os.system / Popen(str) 等）。"""
    src = (ORCH_DIR / "cluster_checks.py").read_text(encoding="utf-8")
    assert "os.system" not in src
    assert "os.popen" not in src
    assert "shell =" not in src


# ── 2-4. 路径穿越 / 文件读取参数全部拒绝 ─────────────────────────────

def test_reject_namespace_path_traversal():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "pod_events", "namespace": "../x"})


def test_reject_pod_proc_environ():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "describe_pod", "namespace": "default",
                             "pod": "/proc/self/environ"})


def test_reject_pod_passwd_traversal():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "describe_pod", "namespace": "default",
                             "pod": "../../etc/passwd"})


def test_reject_pod_with_absolute_path():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "describe_pod", "namespace": "default",
                             "pod": "/etc/shadow"})


def test_reject_pod_with_tilde():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "describe_pod", "namespace": "default",
                             "pod": "~/.kube/config"})


# ── 5. 任意自由 shell 字符串接口不存在/拒绝 ──────────────────────────

def test_free_shell_string_rejected():
    """旧接口的 shell 字符串（含白名单管道 + 文件参数）必须被拒绝，
    不得再进入任何执行路径（原 P1-S1 PoC: kubectl get pods | head /proc/self/environ）。"""
    for cmd in (
        "kubectl get pods | head /proc/self/environ",
        "kubectl get pods | tail /proc/self/environ",
        "kubectl get pods | sort /proc/self/environ",
        "kubectl get pods | grep token /proc/self/environ",
        "kubectl get pods -o wide",
        "kubectl version --client | head -1",
    ):
        assert check_from_hypothesis(cmd) is None, f"shell string must be rejected: {cmd}"


def test_rca_cluster_check_rejects_shell_strings():
    """rca.cluster_check 兼容入口对任意 shell 字符串返回拒绝，不执行。"""
    import rca

    out = rca.cluster_check("kubectl get pods | head /proc/self/environ")
    assert "拒绝" in out or "仅允许" in out, out


# ── 6-10. kubectl 危险子命令永久不可达 ───────────────────────────────

_EXECUTION_VERBS = {"get", "top", "describe"}
_FORBIDDEN_VERBS = {
    "exec", "cp", "port-forward", "proxy", "attach",
    "apply", "create", "delete", "patch", "replace", "edit",
    "drain", "cordon", "taint", "scale", "rollout", "label", "annotate",
}


def test_argv_only_contains_readonly_verbs_for_all_kinds():
    """argv 只能来自静态模板：动词 ⊆ {get, top, describe}，
    exec/cp/port-forward/proxy/apply/create/delete/patch/replace/edit 永远不可达。"""
    for kind in cluster_checks.VALID_KINDS:
        ns = "observability"
        kwargs = {"kind": kind, "namespace": ns}
        if kind == "describe_pod":
            kwargs["pod"] = "pod-abc-123"
        argv = build_argv(parse_cluster_check(kwargs))
        assert argv[0] == "kubectl"
        assert argv[1] in _EXECUTION_VERBS, f"{kind}: unexpected verb {argv[1]}"
        for tok in argv:
            assert tok not in _FORBIDDEN_VERBS, f"{kind}: forbidden verb {tok} in argv"


def test_argv_never_contains_dangerous_verbs_e2e():
    """e2e: 即使 LLM 输出伪造 kind/参数，执行时 argv 也不含危险子命令。"""
    captured = {}

    def fake_run(argv, **kwargs):
        captured["argv"] = list(argv)
        return subprocess.CompletedProcess(argv, 0, stdout="ok", stderr="")

    with patch.object(cluster_checks.subprocess, "run", fake_run):
        run_cluster_check(ClusterCheck(kind="pod_restarts", namespace="default"))
    assert captured["argv"][1] in _EXECUTION_VERBS
    assert not (_FORBIDDEN_VERBS & set(captured["argv"]))


def test_reject_unknown_kind():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "run_shell", "namespace": "default"})


# ── 11. 不允许调用方自定义 -o jsonpath / custom-columns ──────────────

def test_caller_cannot_override_output_jsonpath():
    """调用方传入 output/args/flags 等额外字段一律被忽略，argv 保持静态模板。"""
    captured = {}

    def fake_run(argv, **kwargs):
        captured["argv"] = list(argv)
        return subprocess.CompletedProcess(argv, 0, stdout="p1 p2", stderr="")

    with patch.object(cluster_checks.subprocess, "run", fake_run):
        run_cluster_check(parse_cluster_check({
            "kind": "pod_oom",
            "namespace": "default",
            "output": "jsonpath=/etc/passwd",
            "args": ["exec", "-it", "pod", "--", "cat", "/etc/shadow"],
            "flags": ["--kubeconfig=/tmp/evil"],
        }))
    # jsonpath/custom-columns 只能来自代码模板
    assert "-o" in captured["argv"]
    static_o = {tok for tok in cluster_checks._OUTPUT_TEMPLATES.values() if tok}
    assert captured["argv"][captured["argv"].index("-o") + 1] in static_o


# ── 12-14. flag 注入（--kubeconfig/--token/--server）拒绝 ────────────

def test_reject_kubeconfig_injection():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "pod_events",
                             "namespace": "obs --kubeconfig=/tmp/kc"})


def test_reject_token_injection():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "pod_events", "namespace": "--token=abc"})


def test_reject_server_injection():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "pod_events",
                             "namespace": "--server=https://evil.example"})


def test_reject_pod_flag_injection():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "describe_pod", "namespace": "default",
                             "pod": "-n kube-system"})


# ── 15. 固定模板仍能正常读取 events/pods/nodes/deployments/endpoints ─

_KIND_EXPECTATIONS = {
    "pod_events": ["get", "events", "-n"],
    "pod_restarts": ["get", "pods", "-n"],
    "pod_waiting": ["get", "pods", "-n"],
    "pod_oom": ["get", "pods", "-n"],
    "pod_usage": ["top", "pod", "-n"],
    "node_status": ["get", "nodes"],
    "node_usage": ["top", "node"],
    "deploy_replicas": ["get", "deployment", "-n"],
    "svc_endpoints": ["get", "endpoints", "-n"],
    "describe_pod": ["describe", "pod", "-n"],
}


def test_fixed_templates_execute_readonly_queries():
    """全部 10 种固定检查模板可正常构建并执行（argv 语义与原白名单等价）。"""
    for kind, expect in _KIND_EXPECTATIONS.items():
        kwargs = {"kind": kind, "namespace": "observability"}
        if kind == "describe_pod":
            kwargs["pod"] = "pod-abc-123"
        cc = parse_cluster_check(kwargs)
        argv = build_argv(cc)
        for e in expect:
            assert e in argv, f"{kind}: argv missing {e}: {argv}"
        if kind in ("pod_events", "pod_restarts", "pod_oom",
                    "pod_waiting", "pod_usage", "deploy_replicas", "svc_endpoints"):
            assert "observability" in argv
        if kind == "describe_pod":
            assert "pod-abc-123" in argv


def _fake_run(stdout="", stderr="", returncode=0):
    calls = []

    def fake(argv, **kwargs):
        calls.append({"argv": list(argv), **kwargs})
        return subprocess.CompletedProcess(argv, returncode, stdout=stdout, stderr=stderr)

    fake.calls = calls
    return fake


def test_run_returns_output_and_uses_shell_false():
    AUDIT_LOG.clear()
    fake = _fake_run(stdout="pod-a Running\npod-b Pending\n")
    with patch.object(cluster_checks.subprocess, "run", fake):
        out = run_cluster_check(ClusterCheck(kind="pod_restarts", namespace="default"))
    assert "pod-a" in out
    assert fake.calls and fake.calls[0].get("shell", False) is False, \
        "must run with shell=False"


def test_namespace_defaults():
    AUDIT_LOG.clear()
    fake = _fake_run(stdout="x")
    with patch.object(cluster_checks.subprocess, "run", fake):
        run_cluster_check(ClusterCheck(kind="pod_events"))
    assert DEFAULT_NAMESPACE in fake.calls[0]["argv"]


# ── Python 后处理（原管道工具职责内化）──────────────────────────────

def test_pod_events_tail_postprocess():
    lines = "\n".join(f"event-{i}" for i in range(50))
    with patch.object(cluster_checks.subprocess, "run", _fake_run(stdout=lines)):
        out = run_cluster_check(ClusterCheck(kind="pod_events", namespace="default"))
    out_lines = [ln for ln in out.splitlines() if ln]
    assert len(out_lines) <= 20
    assert out_lines[-1] == "event-49", "tail -20 语义: 保留最后 20 行"


def test_pod_oom_grep_postprocess():
    with patch.object(cluster_checks.subprocess, "run",
                      _fake_run(stdout="OOMKilled Error  normal Running OOMKilled")):
        out = run_cluster_check(ClusterCheck(kind="pod_oom", namespace="default"))
    tokens = [t for t in out.split() if t]
    assert tokens and all("oom" in t.lower() for t in tokens), out


def test_describe_pod_tail_postprocess():
    lines = "\n".join(f"line-{i}" for i in range(100))
    with patch.object(cluster_checks.subprocess, "run", _fake_run(stdout=lines)):
        out = run_cluster_check(ClusterCheck(kind="describe_pod",
                                             namespace="default", pod="p-1"))
    out_lines = [ln for ln in out.splitlines() if ln]
    assert out_lines[-1] == "line-99", "tail -30 语义"


def test_no_awk_or_pipe_tool_in_execution_path():
    """awk 删除：任何 kind 构造出的 argv 都不得包含 awk/tail/head 等管道工具
    （后处理已在 Python 内实现）。"""
    pipe_tools = {"awk", "tail", "head", "grep", "tr", "sort", "wc", "echo", "sh", "bash"}
    for kind in cluster_checks.VALID_KINDS:
        kwargs = {"kind": kind, "namespace": "default"}
        if kind == "describe_pod":
            kwargs["pod"] = "p-1"
        argv = build_argv(parse_cluster_check(kwargs))
        assert not (pipe_tools & set(argv)), f"{kind}: pipe tool in argv: {argv}"


# ── 输出限制 / 超时 / stderr 净化 / 审计 ─────────────────────────────

def test_output_truncated_at_limit():
    AUDIT_LOG.clear()
    big = "x" * (OUTPUT_LIMIT_CHARS * 4)
    with patch.object(cluster_checks.subprocess, "run", _fake_run(stdout=big)):
        out = run_cluster_check(ClusterCheck(kind="pod_restarts", namespace="default"))
    assert len(out) <= OUTPUT_LIMIT_CHARS


def test_timeout_capped():
    fake = _fake_run(stdout="ok")
    with patch.object(cluster_checks.subprocess, "run", fake):
        run_cluster_check(ClusterCheck(kind="node_status"), timeout=999)
    assert fake.calls[0]["timeout"] <= TIMEOUT_CAP


def test_stderr_sanitized_no_exception_dump():
    """kubectl 缺失等异常不得把内部 exception/环境变量 dump 给调用方。"""
    def raising_run(argv, **kwargs):
        raise FileNotFoundError(2, "No such file or directory", "/usr/local/bin/kubectl")

    with patch.object(cluster_checks.subprocess, "run", raising_run):
        out = run_cluster_check(ClusterCheck(kind="node_status"))
    assert "No such file" not in out
    assert "/usr/local/bin" not in out
    assert "Traceback" not in out
    assert "kubectl" in out


def test_audit_record_fields_no_secret():
    AUDIT_LOG.clear()
    with patch.object(cluster_checks.subprocess, "run", _fake_run(stdout="ok")):
        run_cluster_check(ClusterCheck(kind="describe_pod",
                                       namespace="default", pod="pod-1"))
    rec = AUDIT_LOG[-1]
    for field in ("check_kind", "namespace", "pod", "exit_code",
                  "duration_ms", "truncated"):
        assert field in rec, f"audit missing {field}: {rec}"
    assert rec["check_kind"] == "describe_pod"
    # 审计不得记录 stdout/Secret
    assert "ok" not in str(rec)


# ── LLM hypothesis 结构化提取 ────────────────────────────────────────

def test_check_from_structured_hypothesis():
    cc = check_from_hypothesis({"proposed_check": {
        "kind": "pod_events", "namespace": "observability"}})
    assert cc is not None and cc.kind == "pod_events"


def test_check_from_template_string_compat():
    """旧 {kind} 模板引用形式仍可用（映射为结构化 check）。"""
    cc = check_from_hypothesis({"proposed_check": "{pod_events}"})
    assert cc is not None and cc.kind == "pod_events"
    with pytest.raises(InvalidClusterCheck):
        check_from_hypothesis({"proposed_check": "{evil_cmd}"})


def test_check_from_hypothesis_missing_or_garbage():
    assert check_from_hypothesis({}) is None
    assert check_from_hypothesis({"proposed_check": ""}) is None
    assert check_from_hypothesis({"proposed_check": "ps aux | grep oom"}) is None
    assert check_from_hypothesis({"proposed_check": 12345}) is None


def test_describe_pod_requires_pod():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "describe_pod", "namespace": "default"})


def test_non_string_namespace_pod_rejected():
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "pod_events", "namespace": 123})
    with pytest.raises(InvalidClusterCheck):
        parse_cluster_check({"kind": "describe_pod", "namespace": "default",
                             "pod": ["a"]})
