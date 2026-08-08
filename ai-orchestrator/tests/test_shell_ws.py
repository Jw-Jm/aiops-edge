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
    # 不在白名单的 kubectl 操作（如 delete namespace）应禁止执行
    ok, _ = policy.is_whitelisted_for_execute("kubectl delete namespace kube-system")
    assert ok is False
