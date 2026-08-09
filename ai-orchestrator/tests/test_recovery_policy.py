# 恢复白名单安全测试（对应 C6 修复：移除 curl 任意 URL，防 SSRF）。
import pytest
from recovery_policy import check_allowed, DEFAULT_POLICY


def test_allow_safe_recovery():
    ok, reason = check_allowed("kubectl rollout restart deployment/orders")
    assert ok, reason
    ok, _ = check_allowed("kubectl scale deployment/orders --replicas=3")
    assert ok
    ok, _ = check_allowed("systemctl restart nginx")
    assert ok


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
