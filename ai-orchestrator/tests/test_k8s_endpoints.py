"""C2: preflight/execute 端点核心逻辑(审批 + token + 乐观锁 + 审计)

直接调模块函数 + mock 子进程, 不经过 main.py 路由:
- 非 approver 的 403 由 main.py `_require_approver` 在挂载层负责 (见报告 Mount C2 片段);
- 本文件覆盖挂载层之下的全部业务判定: 审批单状态/参数匹配、preflight token、
  乐观锁(resourceVersion)、执行与审计回调。
"""
import pytest

import db_approval
import k8s_actions
from k8s_actions import K8sActionError, make_preflight_token, set_secret

_SECRET = "endpoint-test-secret"

# ApprovalStore P1-R1 后为纯 MySQL（fail-closed，不再内存降级）。本文件是 legacy
# k8s_actions 执行流的端点测试，测试环境无 MySQL → 以共享内存 double 替换
# ApprovalStore（存储替身，不重写被测业务逻辑），使跨实例 create/decide/get 一致。
_shared_mem: dict = {}


class _SharedApprovalStore(db_approval.ApprovalStore):
    """测试替身：仅覆盖持久化方法为共享内存，校验/审计语义沿用生产。"""

    def __init__(self):
        self._mem = _shared_mem

    def _available(self):
        return True

    def degraded(self):
        return False

    def create(self, task):
        self._mem[task["id"]] = dict(task)

    def get(self, task_id):
        return self._mem.get(task_id)

    def list(self):
        return list(self._mem.values())

    def update(self, task_id, **fields):
        from db_approval import _UPDATABLE_COLUMNS
        unknown = set(fields) - _UPDATABLE_COLUMNS
        if unknown:
            raise ValueError(f"approval update has non-whitelisted columns: {sorted(unknown)}")
        if task_id in self._mem:
            self._mem[task_id].update(fields)

    def decide(self, task_id, status, decision_by=""):
        import time
        self.update(task_id, status=status,
                    decided_at=time.strftime("%Y-%m-%dT%H:%M:%SZ"), decision_by=decision_by)


@pytest.fixture(autouse=True)
def _env(monkeypatch):
    set_secret(_SECRET)
    _shared_mem.clear()
    monkeypatch.setattr(db_approval, "ApprovalStore", _SharedApprovalStore)
    # 子进程通道统一 mock: 资源版本固定 + 执行输出固定
    monkeypatch.setattr(k8s_actions, "_run_cmd", lambda cmd, timeout=30: "42")
    yield


def _approved_task(name="p1", ns="ns1", script=None):
    store = db_approval.ApprovalStore()
    store.create({"id": "task-1", "service": ns, "status": "waiting", "plan": "",
                  "script": script or f"kubectl delete pod {name} --grace-period=30 -n {ns}",
                  "risk_score": 0, "risk_reason": "", "diagnosis": "",
                  "report": "", "requester": "u1", "created_at": "2026-01-01T00:00:00Z"})
    store.decide("task-1", "approved", decision_by="u2")
    return "task-1"


def _token(action="delete_pod", kind="pod", namespace="ns1", name="p1", **kw):
    return make_preflight_token(action, kind, namespace, name, **kw)


# ═══════════════ 审批门 (destructive) ═══════════════

def test_destructive_without_approval_rejected():
    """destructive 动作(delete_pod)未走审批单 → 403 拒绝。"""
    with pytest.raises(K8sActionError) as ei:
        k8s_actions.execute_guarded("delete_pod", kind="pod", namespace="ns1", name="p1",
                                    preflight_token=_token(), approval_task_id="")
    assert ei.value.status_code == 403


def test_destructive_with_pending_approval_rejected():
    """审批单存在但未 approved → 拒绝。"""
    db_approval.ApprovalStore().create({"id": "task-pending", "service": "ns1", "status": "waiting",
                            "plan": "", "script": "kubectl delete pod p1 --grace-period=30 -n ns1",
                            "risk_score": 0, "risk_reason": "", "diagnosis": "",
                            "report": "", "requester": "u1", "created_at": "2026-01-01T00:00:00Z"})
    with pytest.raises(K8sActionError) as ei:
        k8s_actions.execute_guarded("delete_pod", kind="pod", namespace="ns1", name="p1",
                                    preflight_token=_token(), approval_task_id="task-pending")
    assert ei.value.status_code == 403


def test_destructive_approval_param_mismatch_rejected():
    """审批单已批准但参数不匹配(目标资源不在审批 script 内) → 拒绝。"""
    _approved_task(name="other-pod")  # script 针对 other-pod
    with pytest.raises(K8sActionError) as ei:
        k8s_actions.execute_guarded("delete_pod", kind="pod", namespace="ns1", name="p1",
                                    preflight_token=_token(), approval_task_id="task-1")
    assert ei.value.status_code == 403


# ═══════════════ preflight token 门 ═══════════════

def test_execute_without_token_rejected():
    _approved_task()
    with pytest.raises(K8sActionError) as ei:
        k8s_actions.execute_guarded("delete_pod", kind="pod", namespace="ns1", name="p1",
                                    preflight_token="", approval_task_id="task-1")
    assert ei.value.status_code == 400


def test_execute_with_tampered_token_rejected():
    _approved_task()
    with pytest.raises(K8sActionError) as ei:
        k8s_actions.execute_guarded("delete_pod", kind="pod", namespace="ns1", name="p1",
                                    preflight_token=_token() + "x", approval_task_id="task-1")
    assert ei.value.status_code == 400


# ═══════════════ 乐观锁 ═══════════════

def test_execute_resource_version_changed_conflict():
    """expected_resourceVersion 与当前不一致 → 409 冲突, 不执行。"""
    _approved_task()
    with pytest.raises(K8sActionError) as ei:
        k8s_actions.execute_guarded("delete_pod", kind="pod", namespace="ns1", name="p1",
                                    preflight_token=_token(), approval_task_id="task-1",
                                    expected_resource_version="99")  # mock 返回 42
    assert ei.value.status_code == 409


# ═══════════════ 通过路径 ═══════════════

def test_nondestructive_happy_path_without_approval():
    """非 destructive(rollout_restart)无需审批单即可执行。"""
    tok = _token(action="rollout_restart", kind="deployment", namespace="ns1", name="svc")
    res = k8s_actions.execute_guarded("rollout_restart", kind="deployment", namespace="ns1",
                                      name="svc", preflight_token=tok,
                                      expected_resource_version="42")
    assert res["ok"] is True
    assert res["output"] == "42"


def test_destructive_happy_path_with_approval_and_audit():
    """destructive 动作: 审批通过 + token 有效 + 版本一致 → 执行并写审计。"""
    _approved_task()
    audit = []
    res = k8s_actions.execute_guarded("delete_pod", kind="pod", namespace="ns1", name="p1",
                                      preflight_token=_token(), approval_task_id="task-1",
                                      expected_resource_version="42",
                                      audit=lambda action, kind, name, out: audit.append(
                                          (action, f"{kind}/{name}", out)))
    assert res["ok"] is True
    assert res["output"] == "42"
    assert audit == [("delete_pod", "pod/p1", "42")], "应写入审计回调"


def test_scale_happy_path():
    tok = _token(action="scale", kind="deployment", namespace="ns1", name="svc", replicas=2)
    res = k8s_actions.execute_guarded("scale", kind="deployment", namespace="ns1", name="svc",
                                      preflight_token=tok, expected_resource_version="42",
                                      extra={"replicas": 2})
    assert res["ok"] is True
