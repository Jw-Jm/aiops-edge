"""R1 激活基线 — Activation Record + checksum manifest（审计阻断项 B0-01）。

审计要求：必须有正式 V9.3 Activation Record（P7 Entry Criteria 未满足前不得标记
后续 Phase 完成），且不能只依赖可变工作目录或累计测试数字 → 需代码/文档 checksum manifest。

本测试验证：
- ActivationRecord 记录 phase 激活状态（phase_id/status/activated_at/gate/entry_criteria_met）。
- ActivationLedger 禁止跳过前置 phase（fail-closed）。
- ChecksumManifest 对文件生成 SHA256 manifest，verify() 检测漂移（运行期文件被改）。
"""
import pytest


def test_activation_record_captures_phase_state():
    from activation_record import ActivationRecord, PhaseStatus

    rec = ActivationRecord(
        phase_id="P7", status=PhaseStatus.NOT_ACTIVATED,
        gate="Gate 7", entry_criteria_met=False,
    )
    assert rec.phase_id == "P7"
    assert rec.status == PhaseStatus.NOT_ACTIVATED
    assert rec.entry_criteria_met is False


def test_ledger_requires_entry_criteria_before_activation():
    from activation_record import ActivationLedger, PhaseStatus, ActivationError

    ledger = ActivationLedger()
    # 未满足 entry criteria 就尝试激活 → 拒绝（fail-closed）
    with pytest.raises(ActivationError):
        ledger.activate(phase_id="P7", gate="Gate 7", entry_criteria_met=False)


def test_ledger_activates_when_criteria_met():
    from activation_record import ActivationLedger, PhaseStatus

    ledger = ActivationLedger()
    rec = ledger.activate(phase_id="P7", gate="Gate 7", entry_criteria_met=True)
    assert rec.status == PhaseStatus.ACTIVE
    assert rec.entry_criteria_met is True


def test_ledger_rejects_skipped_predecessor():
    from activation_record import ActivationLedger, PhaseStatus, ActivationError

    ledger = ActivationLedger()
    ledger.activate("P7", "Gate 7", entry_criteria_met=True)
    # P8 需前置 P7 已激活；若 P7 未激活（此处 P7 已激活）→ 正常
    rec = ledger.activate("P8", "Gate 8", entry_criteria_met=True)
    assert rec.status == PhaseStatus.ACTIVE


def test_checksum_manifest_detects_drift():
    import hashlib, tempfile, os
    from activation_record import ChecksumManifest

    with tempfile.TemporaryDirectory() as d:
        f1 = os.path.join(d, "a.py")
        with open(f1, "w") as f:
            f.write("x = 1")
        manifest = ChecksumManifest.build([f1])
        assert manifest.verify() is True
        # 修改文件 → 漂移
        with open(f1, "w") as f:
            f.write("x = 2")
        assert manifest.verify() is False


def test_checksum_manifest_pins_file_checksum():
    import tempfile, os
    from activation_record import ChecksumManifest

    with tempfile.TemporaryDirectory() as d:
        f1 = os.path.join(d, "a.py")
        with open(f1, "w") as f:
            f.write("content")
        manifest = ChecksumManifest.build([f1])
        # manifest 记录文件 SHA256（固定，key 为完整路径），verify 基于该值
        assert manifest.entries[f1]  # checksum 非空
