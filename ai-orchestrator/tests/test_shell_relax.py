"""命令安全策略：管道放行 + 白名单强制（G5 安全加固后）。

安全修复(G5) 后的预期行为：
1. check_shell_metachars 放行管道（`|`），但拒绝换行/分号/重定向等拼接元字符
2. is_whitelisted_for_execute 允许 kubectl 只读/写白名单命令（含 grep 管道）
3. execute_shell 低层函数强制白名单 + 元字符校验（纵深防御），非白名单命令一律拒绝
4. kubectl exec 仅允许命名空间限定目标 + `--` 后只读诊断命令白名单
"""
import unittest
from shell_policy import ShellPolicy


def test_check_shell_metachars_pipe_allowed_newline_rejected():
    """管道放行；换行/分号等拼接元字符拒绝（防多行拼接绕过白名单）。"""
    policy = ShellPolicy()
    cmd = "kubectl get pods -A | grep -E 'CrashLoopBackOff|OOMKilled'"
    assert policy.check_shell_metachars(cmd) is None, \
        f"管道命令不应被拒绝: {policy.check_shell_metachars(cmd)}"
    # 多行命令含换行 → 拒绝（G5 安全加固：禁止换行拼接绕过）
    multi = "kubectl get deploy -n observability\nkubectl get pods -n observability"
    assert policy.check_shell_metachars(multi) is not None, "换行拼接应被拒绝"
    # 分号拼接 → 拒绝
    semi = "kubectl get pods; cat /etc/shadow"
    assert policy.check_shell_metachars(semi) is not None, "分号拼接应被拒绝"


def test_is_whitelisted_for_execute_pipe_allowed():
    """kubectl 管道命令应被允许执行（含 grep 管道）。"""
    policy = ShellPolicy()
    cmd = "kubectl get pods -A | grep CrashLoopBackOff"
    allowed, cat = policy.is_whitelisted_for_execute(cmd)
    assert allowed, f"kubectl 命令应放行: {cmd}"
    assert cat in ("readonly", "operational")


def test_execute_shell_rejects_non_whitelisted():
    """execute_shell 低层函数强制白名单：非白名单命令（echo 等）一律拒绝（G5 纵深防御）。"""
    from tools import execute_shell
    out = execute_shell("echo 'hello-pipe-test' | grep hello")
    assert "安全策略拒绝" in out, f"非白名单命令应被拒绝: {out!r}"


def test_execute_shell_rejects_metachars():
    """execute_shell 拒绝含拼接元字符的命令（防 `kubectl ...; cat /etc/shadow` 注入）。"""
    from tools import execute_shell
    out = execute_shell("kubectl get pods; cat /etc/shadow")
    assert "安全策略拒绝" in out, f"拼接命令应被拒绝: {out!r}"


def test_exec_single_line_whitelisted_script_allowed():
    """单行白名单脚本应通过策略（管道+换行脚本因含换行被拒绝，属预期安全行为）。"""
    from shell_policy import ShellPolicy
    policy = ShellPolicy()
    script = "kubectl get pods -A | grep CrashLoopBackOff"
    assert policy.check_shell_metachars(script) is None
    allowed, _ = policy.is_whitelisted_for_execute(script)
    assert allowed, "单行管道脚本应通过白名单"


def test_exec_namespace_scoped_readonly_only():
    """kubectl exec 仅允许命名空间限定目标 + 只读诊断命令（G5 收紧）。"""
    policy = ShellPolicy()
    # 允许：命名空间限定 + 只读诊断命令
    ok, cat = policy.is_whitelisted_for_execute("kubectl exec pod/foo -n ns -- cat /etc/hostname")
    assert ok and cat == "write"
    # 拒绝：无命名空间目标
    ok, _ = policy.is_whitelisted_for_execute("kubectl exec pod/foo -- cat /etc/hostname")
    assert not ok, "无命名空间目标的 exec 应被拒绝"
    # 拒绝：非白名单命令（bash/rm/sh）
    ok, _ = policy.is_whitelisted_for_execute("kubectl exec pod/foo -n ns -- bash")
    assert not ok, "exec 内 bash 应被拒绝"
    ok, _ = policy.is_whitelisted_for_execute("kubectl exec pod/foo -n ns -- rm -rf /")
    assert not ok, "exec 内 rm 应被拒绝"
    # 拒绝：敏感路径（cat /etc/shadow）
    ok, _ = policy.is_whitelisted_for_execute("kubectl exec pod/foo -n ns -- cat /etc/shadow")
    assert not ok, "exec 内读取 /etc/shadow 应被拒绝"
    # 拒绝：exec 内嵌套 kubectl（防子串匹配绕过）
    ok, _ = policy.is_whitelisted_for_execute("kubectl exec pod/foo -n ns -- kubectl get pods")
    assert not ok, "exec 内嵌套 kubectl 应被拒绝"


if __name__ == "__main__":
    unittest.main()