# ai-orchestrator/tests/test_orchestrator_rbac.py
"""Orchestrator Kubernetes RBAC 验证。

正式边界：Orchestrator/Investigation Worker 只读（get/list/watch）；
所有 mutation 必须经 query-api → ai-action-executor。历史 grantK8sWrite
compatibility knob 与 grant/revoke-orchestrator-ops.sh 已删除，本测试验证：
- 渲染后的 ai-orchestrator-ops ClusterRole 只读、绝无 mutation verbs；
- deploy/helm/aiops 中不存在 grantK8sWrite；
- grant/revoke-orchestrator-ops.sh 不存在。
"""

import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
CHART = ROOT / "deploy" / "helm" / "aiops"

_MUTATION = {"patch", "create", "delete", "update"}

_SECRETS = [
    "--set", "secrets.jwtSecret=c-jwt-012345678901234567890123456789",
    "--set", "secrets.llmEncryptionKey=c-llm-012345678901234567890123456789",
    "--set", "secrets.internalToken=c-internal-012345678901234567890123456789",
    "--set", "secrets.ingestApiKey=c-ingest",
    "--set", "secrets.clickhousePassword=c-ch",
    "--set", "secrets.mysqlRootPassword=c-root",
    "--set", "secrets.mysqlAppPassword=c-app",
    "--set", "secrets.mysqlMigratorPassword=c-mig",
    "--set", "secrets.hugeGraphPassword=c-hg",
    "--set", "secrets.llmProxyToken=c-proxy",
    "--set", "secrets.llmProviderKeys=deepseek:c-k",
    "--set", "secrets.orchestratorToQueryToken=c-t",
    "--set", "secrets.orchestratorToQuerySigningKey=c-s",
    "--set", "secrets.orchestratorToQueryVerifyKeys=c-v",
    "--set", "secrets.queryToOrchestratorToken=c-t2",
    "--set", "secrets.queryToOrchestratorSigningKey=c-s2",
    "--set", "secrets.queryToOrchestratorVerifyKeys=c-v2",
    "--set", "secrets.executorToken=c-et",
    "--set", "secrets.aiActionExecutorSigningKey=c-es",
    "--set", "secrets.aiActionExecutorVerifyKeys=c-ev",
    "--set", "global.environment=local",
    "--set-string", r"internalTLS.clientSAN=query-api.observability.svc.cluster.local\,ai-orchestrator.observability.svc.cluster.local",
]


@pytest.fixture(scope="module")
def helm_manifest():
    if subprocess.run(["helm", "version", "--short"], capture_output=True).returncode != 0:
        pytest.skip("helm not installed")
    cmd = ["helm", "template", "aiops", str(CHART), "-f", str(CHART / "values-local-validation.yaml")]
    cmd += _SECRETS
    proc = subprocess.run(cmd, capture_output=True, text=True)
    assert proc.returncode == 0, proc.stderr
    return proc.stdout


def _orchestrator_ops_rules(manifest: str) -> list[dict]:
    """解析 manifest 中 ai-orchestrator-ops ClusterRole 的 rules。"""
    try:
        import yaml
    except ImportError:
        return []
    docs = list(yaml.safe_load_all(manifest))
    for doc in docs:
        if doc and doc.get("kind") == "ClusterRole" and doc.get("metadata", {}).get("name") == "ai-orchestrator-ops":
            return doc.get("rules", [])
    return []


def _all_verbs(rules: list[dict]) -> set[str]:
    verbs: set[str] = set()
    for rule in rules:
        verbs.update(rule.get("verbs", []))
    return verbs


def test_orchestrator_ops_cluster_role_is_read_only(helm_manifest):
    """ai-orchestrator-ops 只读：verb ⊆ {get,list,watch}，绝无 mutation verbs。"""
    verbs = _all_verbs(_orchestrator_ops_rules(helm_manifest))
    assert verbs <= {"get", "list", "watch"}, f"orchestrator-ops must be read-only, got {verbs}"
    assert verbs & _MUTATION == set()


def test_no_deployment_patch_rule_anywhere(helm_manifest):
    """渲染结果中不存在对 deployments 的 patch 规则（历史 RBAC toggle 已删）。"""
    for rule in _orchestrator_ops_rules(helm_manifest):
        assert not ("deployments" in rule.get("resources", []) and "patch" in rule.get("verbs", [])), rule


def test_grant_knob_and_scripts_removed():
    """grantK8sWrite knob 与 grant/revoke 脚本都不应存在。"""
    knob = subprocess.run(
        ["rg", "-n", "grantK8sWrite", str(CHART)],
        capture_output=True,
        text=True,
    )
    assert knob.returncode == 1, f"grantK8sWrite must not exist in deploy/helm/aiops:\n{knob.stdout}"
    for name in ("grant-orchestrator-ops.sh", "revoke-orchestrator-ops.sh"):
        assert not (ROOT / "deploy" / "scripts" / name).exists(), name
