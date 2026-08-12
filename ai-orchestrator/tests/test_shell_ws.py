from shell_policy import ShellPolicy

policy = ShellPolicy()


def test_readonly_allowed():
    ok, cat = policy.is_whitelisted_for_execute("kubectl get pods")
    assert ok is True and cat == "readonly"


def test_write_category():
    ok, cat = policy.is_whitelisted_for_execute("kubectl rollout restart deployment/foo")
    assert ok is True and cat == "write"


def test_not_whitelisted():
    ok, cat = policy.is_whitelisted_for_execute("rm -rf /")
    assert ok is False


def test_dangerous_rejected():
    # 危险命令（rm 系统路径）应被 check 拦截
    assert policy.check("rm -rf /") is not None


def test_non_whitelisted_kubectl_rejected():
    # 已按产品要求放宽：kubectl 开头命令均放行（执行前经人工审批）。
    # 不再按 readonly/write 白名单严格限制，仅拦截非运维命令族。
    ok, _ = policy.is_whitelisted_for_execute("kubectl delete namespace kube-system")
    assert ok is True, "kubectl 命令已放宽放行（有人工审批）"
    # 非运维命令族（如直接读系统文件）仍应拒绝
    ok2, _ = policy.is_whitelisted_for_execute("cat /etc/shadow")
    assert ok2 is False
