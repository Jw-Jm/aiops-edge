"""Issue6: 放宽命令安全策略——处置/工作流命令支持管道、重定向、换行。

验证:
1. check_shell_metachars 不再拒绝含 |/; / 换行的命令
2. is_whitelisted_for_execute 允许 kubectl/curl 开头的命令（含管道）
3. execute_shell 能真正执行含管道的命令（shell=True 生效）
4. execute_suggestion 的管道命令可通过策略
"""
import unittest
from shell_policy import ShellPolicy


def test_check_shell_metachars_relaxed():
    """含管道/换行的命令不再被元字符策略拒绝。"""
    policy = ShellPolicy()
    cmd = "kubectl get pods -A | grep -E 'CrashLoopBackOff|OOMKilled'"
    assert policy.check_shell_metachars(cmd) is None, \
        f"管道命令不应被拒绝: {policy.check_shell_metachars(cmd)}"
    # 多行命令也应放行
    multi = "kubectl get deploy -n observability\nkubectl get pods -n observability"
    assert policy.check_shell_metachars(multi) is None


def test_is_whitelisted_for_execute_relaxed():
    """kubectl 管道命令应被允许执行（含 grep 管道）。"""
    policy = ShellPolicy()
    cmd = "kubectl get pods -A | grep CrashLoopBackOff"
    allowed, cat = policy.is_whitelisted_for_execute(cmd)
    assert allowed, f"kubectl 命令应放行: {cmd}"
    assert cat in ("readonly", "operational")


def test_execute_shell_with_pipe():
    """execute_shell 应真正执行含管道的命令（shell=True 生效），返回 grep 结果。"""
    from tools import execute_shell
    # 用 echo | grep 验证管道真的生效（非字面量 arg）
    out = execute_shell("echo 'hello-pipe-test' | grep hello")
    assert "hello-pipe-test" in out, f"管道命令应执行成功: {out!r}"


def test_execute_suggestion_script_allowed():
    """execute_suggestion 生成的带管道/换行的脚本应能通过策略。"""
    from orchestrator import BrainOrchestrator
    b = BrainOrchestrator()
    script = (
        "kubectl get pods -A | grep -E 'CrashLoopBackOff|OOMKilled'\n"
        "kubectl describe pod -l app=order-svc -A | tail -60"
    )
    # 直接调用策略校验（不真正执行 kubectl，仅验证能通过安全闸门）
    from shell_policy import ShellPolicy
    policy = ShellPolicy()
    assert policy.check_shell_metachars(script) is None
    allowed, _ = policy.is_whitelisted_for_execute(script)
    assert allowed, "管道+换行脚本应通过白名单"


if __name__ == "__main__":
    unittest.main()
