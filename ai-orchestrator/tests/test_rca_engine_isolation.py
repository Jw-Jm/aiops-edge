"""Canonical RCA engine 装配契约（P1-A1 迁移）。

P1-A1 前：rca_engine 包通过 feature flag 动态加载 rca_engine_legacy.py 提供
RcaEngine（production fail-closed 不带 legacy；local 才兼容）。
P1-A1 后：Phase 9 RcaEngine 已提升为 rca_engine.phase9_engine（静态导出）。
本测试断言：
- production main/composition import 得到的是**同一个 canonical 实现**；
- 不存在 legacy fallback / 环境开关选择引擎（无 _legacy / _load_legacy / compat flags）；
- RcaEngine 与 phase9_engine.RcaEngine 是同一 class 对象（不是 adapter）。
"""

from __future__ import annotations

import os
import subprocess
import sys


def test_canonical_rca_engine_is_statically_exported():
    """from rca_engine import RcaEngine 必须与 phase9_engine.RcaEngine 是同一实现。"""
    from rca_engine import EvidenceScopeMismatch, HypothesisEvaluation, RcaComputation, RcaEngine
    from rca_engine import phase9_engine

    assert RcaEngine is phase9_engine.RcaEngine, "RcaEngine must statically alias the canonical engine"
    assert EvidenceScopeMismatch is phase9_engine.EvidenceScopeMismatch
    assert RcaComputation is phase9_engine.RcaComputation
    assert HypothesisEvaluation is phase9_engine.HypothesisEvaluation


def test_production_import_assembles_canonical_engine_without_legacy_fallback():
    """production 装配只能指向 canonical engine；不得存在 legacy fallback 模块/开关。"""
    code = (
        "import sys; import rca_engine; "
        "from rca_engine import RcaEngine; "
        "from rca_engine import phase9_engine; "
        "assert RcaEngine is phase9_engine.RcaEngine; "
        "assert not hasattr(rca_engine, '_legacy'); "
        "assert not hasattr(rca_engine, '_load_legacy'); "
        "assert not hasattr(rca_engine, '_legacy_compat_enabled'); "
        "assert '_aiops_rca_engine_legacy' not in sys.modules; "
        "assert RcaEngine.__name__ == 'RcaEngine'"
    )
    env = os.environ.copy()
    env["AIOPS_ENV"] = "production"
    env.pop("AIOPS_DEPLOYMENT_MODE", None)
    subprocess.run([sys.executable, "-c", code], check=True, env=env)


def test_engine_identity_does_not_depend_on_environment_flags():
    """RcaEngine 不再受环境标志/模式影响（无新旧实现切换，无 feature-flag 分支）。"""
    import rca_engine
    from rca_engine import RcaEngine
    from rca_engine import phase9_engine

    # 无论环境如何，包装配的 RcaEngine 恒为 canonical（模块级静态绑定）
    assert rca_engine.RcaEngine is RcaEngine is phase9_engine.RcaEngine
    # legacy compat flags 已物理删除——设置它们也不得引入任何分支行为（无该实现可切换）
    env = os.environ.copy()
    env["AIOPS_LEGACY_RCA_COMPAT"] = "1"
    env["AIOPS_ALLOW_LEGACY_COMPAT_IN_PRODUCTION"] = "1"
    code = (
        "import os; os.environ['AIOPS_ENV']='production'; "
        "from rca_engine import RcaEngine; "
        "from rca_engine import phase9_engine; "
        "assert RcaEngine is phase9_engine.RcaEngine; "
        "print('canonical')"
    )
    proc = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, env=env)
    assert proc.returncode == 0, proc.stderr
    assert "canonical" in proc.stdout
