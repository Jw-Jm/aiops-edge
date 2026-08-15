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
    # P0 修复后: is_whitelisted_for_execute 恢复参数黑名单 + 白名单整段校验。
    # kubectl delete 属危险参数黑名单 → 拒绝；直接读系统文件同样拒绝。
    ok, _ = policy.is_whitelisted_for_execute("kubectl delete namespace kube-system")
    assert ok is False, "kubectl delete 在危险参数黑名单内，应拒绝"
    ok2, _ = policy.is_whitelisted_for_execute("cat /etc/shadow")
    assert ok2 is False
