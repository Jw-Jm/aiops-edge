"""marketplace 安装/卸载 + ECDSA 签名三态校验测试"""
import os
import shutil
import asyncio

import pytest

from marketplace import install, uninstall, list_installed, verify_signature
from skill_loader import user_skills_dir
from skill_registry import SkillRegistry

FIXTURES = os.path.join(os.path.dirname(__file__), "fixtures", "market")


@pytest.fixture(autouse=True)
def env(monkeypatch, tmp_path):
    """隔离数据目录（user skills + market.db），并保证内置工具/技能已注册。"""
    monkeypatch.setenv("AIOPS_DATA_DIR", str(tmp_path / "data"))
    monkeypatch.setenv("MARKETPLACE_REQUIRE_SIGNED", "0")
    from skills import init_skills
    init_skills()


def _copy_fixture(name, dest_dir):
    src = os.path.join(FIXTURES, name)
    dst = os.path.join(str(dest_dir), name)
    shutil.copytree(src, dst, symlinks=True)  # 保留符号链接（路径逃逸 fixture 依赖）
    return dst


# ── verify_signature 三态 ───────────────────────────────────────────────

def test_verify_signature_states():
    assert verify_signature(os.path.join(FIXTURES, "signed-pack")) == "verified"
    assert verify_signature(os.path.join(FIXTURES, "unsigned-pack")) == "unsigned"
    assert verify_signature(os.path.join(FIXTURES, "tampered-pack")) == "failed"


def test_verify_signature_tampered_content_fails(tmp_path):
    src = _copy_fixture("signed-pack", tmp_path)
    with open(os.path.join(src, "SKILL.md"), "a", encoding="utf-8") as f:
        f.write("\n# 被篡改\n")
    assert verify_signature(src) == "failed"


# ── install ─────────────────────────────────────────────────────────────

def test_install_requires_admin(tmp_path):
    src = _copy_fixture("unsigned-pack", tmp_path)
    with pytest.raises(PermissionError):
        install(src, as_admin=False)


def test_install_signed_pack_reloads(tmp_path):
    src = _copy_fixture("signed-pack", tmp_path)
    result = install(src, as_admin=True)
    assert result["pack_id"] == "signed-pack"
    assert result["signature_state"] == "verified"
    assert result["skills"] == ["SKILL.md"]

    installed = list_installed()
    assert any(p["pack_id"] == "signed-pack" for p in installed)

    # reload 生效: 新 skill 出现在 SkillRegistry
    s = SkillRegistry.get("skill.market_demo")
    assert s is not None and s.description == "市场安装示例技能，引用内置 query_metrics 工具"

    # 卸载后文件消失且 skill 移除
    uninstall("signed-pack")
    assert not os.path.exists(os.path.join(user_skills_dir(), "signed-pack"))
    assert SkillRegistry.get("skill.market_demo") is None
    assert all(p["pack_id"] != "signed-pack" for p in list_installed())


def test_install_unsigned_allowed_when_not_required(tmp_path):
    src = _copy_fixture("unsigned-pack", tmp_path)
    result = install(src, as_admin=True)
    assert result["signature_state"] == "unsigned"
    assert "source_type" not in result
    assert "source_type" not in list_installed()[0]


def test_install_records_optional_source_type(tmp_path):
    """安装来源类型需随安装记录保留，供已安装列表展示和审计。"""
    src = _copy_fixture("unsigned-pack", tmp_path)

    result = install(src, as_admin=True, source_type="local")

    assert result["source_type"] == "local"
    installed = list_installed()
    assert len(installed) == 1
    assert installed[0]["pack_id"] == "unsigned-pack"
    assert installed[0]["source_type"] == "local"


@pytest.mark.parametrize("source_type", [["local"], "remote"])
def test_install_rejects_invalid_source_type_before_creating_pack(tmp_path, source_type):
    src = _copy_fixture("unsigned-pack", tmp_path)

    with pytest.raises(ValueError, match="source_type"):
        install(src, as_admin=True, source_type=source_type)

    assert not os.path.exists(os.path.join(user_skills_dir(), "unsigned-pack"))
    assert list_installed() == []


def test_install_rejects_conflicting_source_type_before_creating_pack(tmp_path):
    src = _copy_fixture("unsigned-pack", tmp_path)

    with pytest.raises(ValueError, match="source_type"):
        install(src, as_admin=True, source_type="git")

    assert not os.path.exists(os.path.join(user_skills_dir(), "unsigned-pack"))
    assert list_installed() == []


def test_marketplace_install_forwards_source_type_from_request(monkeypatch):
    """安装 API 必须把 AiTools 传入的 source_type 交给 marketplace。"""
    monkeypatch.setenv("INTERNAL_TOKEN", "test-token")
    import main
    import marketplace
    from starlette.requests import Request

    captured = {}

    def fake_install(source, as_admin=False, source_type=None):
        captured.update(source=source, as_admin=as_admin, source_type=source_type)
        return {"pack_id": "demo-pack", "signature_state": "unsigned", "skills": []}

    monkeypatch.setattr(marketplace, "install", fake_install)
    request = Request({
        "type": "http", "method": "POST", "path": "/api/v1/ai/marketplace/install",
        "headers": [(b"x-internal-token", b"test-token"), (b"x-internal-role", b"admin")],
    })

    result = asyncio.run(main.marketplace_install(
        request, {"source": "https://example.invalid/demo.git", "source_type": "git"}))

    assert result["pack_id"] == "demo-pack"
    assert captured == {
        "source": "https://example.invalid/demo.git", "as_admin": True, "source_type": "git",
    }


def test_install_tampered_rejected(tmp_path):
    src = _copy_fixture("tampered-pack", tmp_path)
    with pytest.raises(ValueError, match="签名"):
        install(src, as_admin=True)


def test_install_tampered_content_rejected(tmp_path):
    src = _copy_fixture("signed-pack", tmp_path)
    with open(os.path.join(src, "SKILL.md"), "a", encoding="utf-8") as f:
        f.write("\n# 被篡改\n")
    with pytest.raises(ValueError, match="签名"):
        install(src, as_admin=True)


def test_install_escape_rejected(tmp_path):
    src = _copy_fixture("escape-pack", tmp_path)
    with pytest.raises(ValueError, match="路径逃逸"):
        install(src, as_admin=True)


def test_install_duplicate_pack_id_rejected(tmp_path):
    first = _copy_fixture("signed-pack", tmp_path / "a")
    install(first, as_admin=True)
    second = _copy_fixture("signed-pack", tmp_path / "b")
    with pytest.raises(ValueError, match="已安装"):
        install(second, as_admin=True)


def test_install_require_signed_env_gate(tmp_path, monkeypatch):
    monkeypatch.setenv("MARKETPLACE_REQUIRE_SIGNED", "1")
    src = _copy_fixture("unsigned-pack", tmp_path)
    with pytest.raises(ValueError, match="签名"):
        install(src, as_admin=True)


def test_install_no_skill_md_rejected(tmp_path):
    empty = tmp_path / "empty-pack"
    os.makedirs(empty)
    with pytest.raises(ValueError, match="SKILL.md"):
        install(str(empty), as_admin=True)


def test_install_unknown_source_rejected():
    with pytest.raises(ValueError, match="来源"):
        install("/nonexistent/path/pack.tar.gz", as_admin=True)


# ── uninstall / list ────────────────────────────────────────────────────

def test_uninstall_unknown_pack_raises():
    with pytest.raises(ValueError, match="未安装"):
        uninstall("never-installed")


def test_list_installed_empty(tmp_path, monkeypatch):
    monkeypatch.setenv("AIOPS_DATA_DIR", str(tmp_path / "data2"))
    assert list_installed() == []
