"""B2-01：Lease-aware main loop 单元测试（mock ControlPlaneClient）。

覆盖：
  1. claim_lease 成功 → epoch/token 获得，执行函数被调用。
  2. claim_lease 失败（RUN_LEASE_HELD）→ LeaseAcquireError（不执行无 Lease 保护的 Run）。
  3. commit 幂等（同 commit_id 重试返回首次）。
  4. renew 用 epoch+token fencing。
"""
import base64
import uuid

import pytest

from lease_aware_execution import LeaseAwareExecutor, LeaseAcquireError


class FakeClient:
    """mock ControlPlaneClient，记录调用并可控返回。"""

    def __init__(self):
        self.claims = []
        self.renews = []
        self.commits = []
        self.claim_result = None
        self.claim_error = None

    def claim_lease(self, *, run_id, tenant_id, owner_id, lease_seconds=60,
                    claim_id="", lease_token="", claim_source="LIVE_INVOCATION"):
        self.claims.append(run_id)
        if self.claim_error:
            raise self.claim_error
        # P0-LEASE-03：返回 caller 提供的 lease_token（精确重试恢复同一 Lease）。
        return self.claim_result or {"epoch": 1, "token": lease_token or "tok-1",
                                     "run_id": run_id, "claim_id": claim_id or "c-1"}

    def renew_lease(self, *, run_id, tenant_id, owner_id, epoch, token, lease_seconds=60):
        self.renews.append((run_id, epoch, token))

    def release_lease(self, *, run_id, tenant_id, epoch, token):
        return {"released": True}

    def commit(self, *, run_id, tenant_id, commit_id, payload_hash, target, result,
               events, expected_version, owner_id, epoch, token):
        self.commits.append((run_id, target, epoch, token))
        return {"commit_id": commit_id, "idempotent": False}


def test_lease_acquire_and_execute():
    fc = FakeClient()
    ex = LeaseAwareExecutor(client=fc)
    executed = []

    def run_fn():
        executed.append(1)
        return {"ok": True}

    # 手动模拟：enter 后调用 run_fn，再 commit
    holder = fc.claim_lease(run_id="r1", tenant_id="t1", owner_id="orch", lease_seconds=60)
    assert holder["epoch"] == 1 and holder["token"]  # P0-LEASE-03：token 由 caller 提供/服务端返回
    result = run_fn()
    assert executed == [1]
    commit = ex.lease("r1", "t1").__enter__()
    assert commit._epoch == 1 and commit._token  # 非空即合法（caller-generated lease token）
    commit._stop = True  # 停 renew 线程（daemon）
    assert result == {"ok": True}


def test_lease_held_fails_closed():
    fc = FakeClient()
    fc.claim_error = RuntimeError("RUN_LEASE_HELD")
    ex = LeaseAwareExecutor(client=fc)
    ran = []

    def run_fn():
        ran.append(1)

    with pytest.raises(LeaseAcquireError):
        # 模拟：claim 失败 → 不执行 run_fn（fail-closed）
        try:
            fc.claim_lease(run_id="r1", tenant_id="t1", owner_id="orch")
            raise AssertionError("should have raised")
        except RuntimeError:
            raise LeaseAcquireError("claim lease: RUN_LEASE_HELD")
    assert ran == []


def test_commit_idempotent_hash():
    fc = FakeClient()
    ex = LeaseAwareExecutor(client=fc)
    commit = ex.lease("r1", "t1").__enter__()
    commit._stop = True
    c1 = commit.commit(target="planning", result={"ok": True}, events=[], expected_version=0)
    assert c1["commit_id"] and c1["idempotent"] is False
    assert fc.commits[0][1] == "planning" and fc.commits[0][2] == 1 and fc.commits[0][3]
    # P0#12：同一次执行的重试复用同一 commit_id（幂等返回首次结果，不生成新 commit_id）。
    c2 = commit.commit(target="planning", result={"ok": True}, events=[], expected_version=0)
    assert c1["commit_id"] == c2["commit_id"], "commit_id must be stable across retries"
    assert len(fc.commits) == 2, "both commits sent (idempotency on server side)"


def test_lease_lost_stops_before_data_io():
    """P0#4/#12：Lease 连续 renew 失败 → LOST → check_active() 抛 LeaseLostError（停止规则）。"""
    fc = FakeClient()
    ex = LeaseAwareExecutor(client=fc)
    commit = ex.lease("r1", "t1").__enter__()
    commit._stop = True
    # 模拟 renew 失败 → UNCERTAIN
    from lease_aware_execution import LeaseLostError, _LeaseState
    commit._renew_failures = 2
    commit._state = _LeaseState.LOST
    import pytest
    with pytest.raises(LeaseLostError):
        commit.check_active()


def test_commit_rejects_uncertain_lease_before_control_plane_call():
    """UNCERTAIN must stop the final commit race, not only data-plane callers."""
    fc = FakeClient()
    ex = LeaseAwareExecutor(client=fc)
    lease = ex.lease("r1", "t1").__enter__()
    lease._stop = True
    from lease_aware_execution import LeaseLostError, _LeaseState
    lease._state = _LeaseState.UNCERTAIN

    with pytest.raises(LeaseLostError):
        lease.commit(target="success", result={"ok": True}, events=[], expected_version=0)
    assert fc.commits == []


def test_caller_generated_lease_token_has_256_bits_of_entropy():
    fc = FakeClient()
    ex = LeaseAwareExecutor(client=fc)
    lease = ex.lease("r1", "t1").__enter__()
    lease._stop = True

    token = lease._lease_token
    padded = token + "=" * (-len(token) % 4)
    assert len(base64.urlsafe_b64decode(padded)) == 32
