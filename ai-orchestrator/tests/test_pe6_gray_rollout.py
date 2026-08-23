"""PE.6 灰度执行阶段机 — TDD 测试（V9.3 Production Enablement）。

覆盖 PE.6：
- T1 从 Stage0（dry-run）开始
- T2 每阶段闸门：allowed → 推进；denied → 终止（停止）
- T3 failed → 终止（回滚）
- T4 全阶段通过 → completed
"""
from __future__ import annotations

import pytest

from gray_rollout import GrayRollout, RolloutDenied


@pytest.fixture
def rollout():
    return GrayRollout()


# ═══════════════════════════════════════════════════════
#  T1 Stage0 开始
# ═══════════════════════════════════════════════════════

class TestT1Start:
    def test_starts_at_stage0(self, rollout):
        r = rollout.start(contract_id="c-1")
        assert r.current_stage == "stage0_dry_run"
        assert r.status == "running"


# ═══════════════════════════════════════════════════════
#  T2 每阶段闸门
# ═══════════════════════════════════════════════════════

class TestT2Gate:
    def test_allowed_advances(self, rollout):
        r = rollout.start(contract_id="c-1")
        r = rollout.advance(r.rollout_id, allowed=True)
        assert r.current_stage == "stage1_single"
        r = rollout.advance(r.rollout_id, allowed=True)
        assert r.current_stage == "stage2_restricted"
        r = rollout.advance(r.rollout_id, allowed=True)
        assert r.current_stage == "stage3_full"

    def test_denied_stops(self, rollout):
        r = rollout.start(contract_id="c-1")
        r = rollout.advance(r.rollout_id, allowed=True)
        with pytest.raises(RolloutDenied):
            rollout.advance(r.rollout_id, allowed=False)  # 闸门失败 → 停止
        assert r.status == "denied"


# ═══════════════════════════════════════════════════════
#  T3 failed → 终止
# ═══════════════════════════════════════════════════════

class TestT3Failed:
    def test_failed_terminates(self, rollout):
        r = rollout.start(contract_id="c-1")
        r = rollout.mark_failed(r.rollout_id)
        assert r.status == "failed"
        # 失败后不能继续推进
        with pytest.raises(RolloutDenied):
            rollout.advance(r.rollout_id, allowed=True)


# ═══════════════════════════════════════════════════════
#  T4 全阶段通过 → completed
# ═══════════════════════════════════════════════════════

class TestT4Completed:
    def test_all_stages_completes(self, rollout):
        r = rollout.start(contract_id="c-1")
        for _ in range(4):  # stage0→1→2→3
            r = rollout.advance(r.rollout_id, allowed=True)
        assert r.status == "completed"
        assert rollout.is_completed(r.rollout_id) is True
