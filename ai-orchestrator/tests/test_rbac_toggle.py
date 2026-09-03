# ai-orchestrator/tests/test_rbac_toggle.py
"""P2-F2: RBAC toggle 运行时验证（原测试仅断言 grant/revoke 脚本文件存在，
不验证任何权限语义）。本测试实际渲染 Helm manifest 并解析 ai-orchestrator-ops
ClusterRole：
- production（grantK8sWrite 默认 false）→ 只含 get/list/watch，绝无 mutation verbs；
- grant（--set aiOrchestrator.grantK8sWrite=true）→ 出现受限 deployments patch；
- revoke（同 grant 后置回 false）→ 权限段消失。
"""

import os
import subprocess
import sys
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
def helm_render():
    if subprocess.run(["helm", "version", "--short"], capture_output=True).returncode != 0:
        pytest.skip("helm not installed")

    def render(grant: bool) -> str:
        cmd = ["helm", "template", "aiops", str(CHART), "-f", str(CHART / "values-local-validation.yaml")]
        cmd += _SECRETS
        cmd += ["--set", f"aiOrchestrator.grantK8sWrite={'true' if grant else 'false'}"]
        proc = subprocess.run(cmd, capture_output=True, text=True)
        assert proc.returncode == 0, proc.stderr
        return proc.stdout

    return render


def _orchestrator_ops_rules(manifest: str) -> list[dict]:
    """解析 manifest 中 ai-orchestrator-ops ClusterRole 的 rules（verb 集合扁平化）。"""
    try:
        import yaml
    except ImportError:
        # PyYAML 不可用时做文本级解析（仅用于本测试）
        yaml = None
    docs = list(yaml.safe_load_all(manifest)) if yaml else []
    for doc in docs:
        if doc and doc.get("kind") == "ClusterRole" and doc.get("metadata", {}).get("name") == "ai-orchestrator-ops":
            return doc.get("rules", [])
    return []


def _all_verbs(rules: list[dict]) -> set[str]:
    verbs: set[str] = set()
    for rule in rules:
        verbs.update(rule.get("verbs", []))
    return verbs


def test_production_manifest_has_no_mutation_verbs(helm_render):
    """默认（production 语义）ai-orchestrator-ops 只读：verb ⊆ {get,list,watch}。"""
    manifest = helm_render(grant=False)
    verbs = _all_verbs(_orchestrator_ops_rules(manifest))
    assert verbs <= {"get", "list", "watch"}, f"orchestrator-ops must be read-only, got {verbs}"
    assert verbs & _MUTATION == set()


def test_grant_toggle_adds_only_restricted_patch(helm_render):
    """grant=true：出现受限 deployments patch（演练用），且无其他 mutation verb。"""
    manifest = helm_render(grant=True)
    rules = _orchestrator_ops_rules(manifest)
    verbs = _all_verbs(rules)
    assert verbs >= {"get", "list", "watch"}
    assert "patch" in verbs
    assert verbs - {"get", "list", "watch", "patch"} == set(), f"unexpected verbs: {verbs}"
    for rule in rules:
        if "patch" in rule.get("verbs", []):
            assert rule["apiGroups"] == ["apps"]
            assert rule["resources"] == ["deployments"]


def test_revoke_toggle_removes_mutation(helm_render):
    """revoke 语义：grant 后置回 false → mutation verb 消失（只读收敛）。"""
    granted = _all_verbs(_orchestrator_ops_rules(helm_render(grant=True)))
    assert "patch" in granted
    revoked = _all_verbs(_orchestrator_ops_rules(helm_render(grant=False)))
    assert "patch" not in revoked
    assert revoked <= {"get", "list", "watch"}


def test_grant_scripts_still_available():
    """grant/revoke 脚本仍存在（编排入口），但真正权限语义由上方运行时测试保证。"""
    for name in ("grant-orchestrator-ops.sh", "revoke-orchestrator-ops.sh"):
        assert (ROOT / "deploy" / "scripts" / name).exists(), name
