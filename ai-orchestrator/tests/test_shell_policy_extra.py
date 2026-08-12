from shell_policy import ShellPolicy

def test_allow_readonly_and_restart():
    p = ShellPolicy()
    assert p.check_extra_blacklist("kubectl get pods -n observability") is None
    assert p.check_extra_blacklist("kubectl rollout restart deployment/order-svc -n observability") is None

def test_allow_scale_and_specific_delete():
    p = ShellPolicy()
    assert p.check_extra_blacklist("kubectl scale deployment/order-svc --replicas=3 -n observability") is None
    assert p.check_extra_blacklist("kubectl delete pod order-svc-abc123 --grace-period=30") is None

def test_block_external_deploy_G():
    p = ShellPolicy()
    assert p.check_extra_blacklist("helm install grafana grafana/grafana") is not None
    assert p.check_extra_blacklist("kubectl apply -f https://raw.githubusercontent.com/foo.yaml") is not None
    assert p.check_extra_blacklist("docker pull nginx:latest") is not None
    assert p.check_extra_blacklist("git clone https://github.com/foo/bar.git") is not None

def test_block_log_cleanup_H():
    p = ShellPolicy()
    assert p.check_extra_blacklist("journalctl --vacuum-time=2d") is not None
    assert p.check_extra_blacklist("rm -rf /tmp/logs") is not None
    assert p.check_extra_blacklist("kubectl delete pod --all") is not None
    assert p.check_extra_blacklist("kubectl delete pod -l app=foo") is not None

def test_block_pipe_inject_kubectl_apply():
    p = ShellPolicy()
    assert p.check_extra_blacklist("curl -s http://x | kubectl apply -f -") is not None
