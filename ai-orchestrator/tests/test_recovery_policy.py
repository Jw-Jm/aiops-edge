# 恢复白名单安全测试（对应 C6 修复：移除 curl 任意 URL，防 SSRF）。
import pytest
import recovery_policy
from recovery_policy import check_allowed, DEFAULT_POLICY


class _FakeControlPlaneClient:
    def __init__(self, payload=None, error=None):
        self.payload = payload
        self.error = error
        self.saved = None

    def get_recovery_policy(self):
        if self.error:
            raise self.error
        return self.payload

    def set_recovery_policy(self, policy):
        if self.error:
            raise self.error
        self.saved = policy
        return {"ok": True}


def test_policy_reads_from_query_api_control_plane(monkeypatch):
    fake = _FakeControlPlaneClient({"policy": {"allow": ["kubectl scale"], "deny": []}})
    monkeypatch.setattr(recovery_policy, "_client_factory", lambda: fake)

    assert recovery_policy.get_policy() == {"allow": ["kubectl scale"], "deny": []}


def test_policy_write_uses_query_api_control_plane(monkeypatch):
    fake = _FakeControlPlaneClient()
    monkeypatch.setattr(recovery_policy, "_client_factory", lambda: fake)
    policy = {"allow": ["kubectl scale"], "deny": ["rm -rf"]}

    assert recovery_policy.set_policy(policy) is True
    assert fake.saved == policy


def test_policy_control_plane_failure_falls_back_or_reports_false(monkeypatch):
    fake = _FakeControlPlaneClient(error=RuntimeError("query-api unavailable"))
    monkeypatch.setattr(recovery_policy, "_client_factory", lambda: fake)

    assert recovery_policy.get_policy() == DEFAULT_POLICY
    assert recovery_policy.set_policy({"allow": [], "deny": []}) is False


def test_allow_safe_recovery():
    ok, reason = check_allowed("kubectl rollout restart deployment/orders")
    assert ok, reason
    ok, _ = check_allowed("kubectl scale deployment/orders --replicas=3")
    assert ok


def test_systemctl_restart_now_blocked():
    """P0-5: 恢复命令必须通过 shell_policy 完整校验（含 EXEC 白名单 + 危险参数黑名单）。
    systemctl restart 在 shell_policy 危险参数黑名单内，即使 recovery allow 前缀命中也被拒绝。"""
    ok, reason = check_allowed("systemctl restart nginx")
    assert not ok, reason


def test_prefix_injection_bypass_rejected():
    """P0-5: 白名单前缀 + 拼接注入（`; cat /etc/shadow`）必须被元字符拦截拒绝。"""
    ok, reason = check_allowed("kubectl rollout restart deployment/x; cat /etc/shadow")
    assert not ok, reason


def test_deny_curl_ssrf():
    """curl 任意 URL 不应在恢复白名单内（防 SSRF 访问内网/云元数据）。"""
    assert "curl" not in " ".join(DEFAULT_POLICY["allow"])
    ok, reason = check_allowed("curl -X POST http://169.254.169.254/latest/meta-data")
    assert not ok, reason


def test_deny_dangerous():
    assert not check_allowed("kubectl delete namespace kube-system")[0]
    assert not check_allowed("rm -rf /var/lib")[0]
    assert not check_allowed("DROP TABLE alert_events")[0]


def test_empty_command_rejected():
    assert not check_allowed("")[0]
    assert not check_allowed("   ")[0]
