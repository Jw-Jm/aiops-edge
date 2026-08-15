# 恢复白名单安全测试（对应 C6 修复：移除 curl 任意 URL，防 SSRF）。
import pytest
from recovery_policy import check_allowed, DEFAULT_POLICY


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
