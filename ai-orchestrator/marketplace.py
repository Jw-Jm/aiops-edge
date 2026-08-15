"""marketplace: 安装来源 local|tarball|git; ECDSA 签名三态校验; 卸载; 热重载。

安装流程: staging → 逃逸检查 → 唯一性 → 签名校验 → rename 落盘 → 记库 → reload。
安全决策: 外部 skill 的 tools 只能引用已有注册工具名（skill_loader 校验），不执行外部代码。
"""
import base64
import hashlib
import json
import os
import re
import shutil
import sqlite3
import subprocess
import tarfile
import tempfile
from datetime import datetime, timezone

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec, utils

from skill_loader import user_skills_dir

# 市场签名策略（MARKETPLACE_REQUIRE_SIGNED=1 时拒绝未签名包；install 内按实时 env 判定）
REQUIRE_SIGNED = os.environ.get("MARKETPLACE_REQUIRE_SIGNED", "0") == "1"

_SIGNATURE_FILE = "signature.json"
_SIGNATURE_SCHEMA = "algo/sig/pub_key"


# ── 路径安全 ────────────────────────────────────────────────────────────

def _is_within(base: str, target: str) -> bool:
    """target 的 realpath 必须落在 base 的 realpath 之内。"""
    b = os.path.realpath(base)
    t = os.path.realpath(target)
    try:
        return os.path.commonpath([b, t]) == b
    except ValueError:
        return False


def _scan_skills(pack_dir: str) -> list:
    """递归查找 pack 内全部 SKILL.md（followlinks=True 以暴露目录符号链接逃逸）。"""
    found = []
    for root, _dirs, files in os.walk(pack_dir, followlinks=True):
        for fn in files:
            if fn == "SKILL.md":
                found.append(os.path.join(root, fn))
    return found


# ── 安装来源获取 ────────────────────────────────────────────────────────

def _fetch_to_staging(source: str) -> str:
    """将 local 目录 / tarball / git URL 拉到 staging，返回 pack 根目录。"""
    staging_base = tempfile.mkdtemp(prefix="market-stage-")
    try:
        if os.path.isdir(source):
            name = os.path.basename(os.path.abspath(source).rstrip("/")) or "pack"
            pack = os.path.join(staging_base, name)
            shutil.copytree(os.path.abspath(source), pack, symlinks=True)
            return pack
        if source.endswith((".tar.gz", ".tgz", ".tar")):
            if not os.path.isfile(source):
                raise ValueError(f"不支持的安装来源或文件不存在: {source}")
            name = re.sub(r"\.(tar\.gz|tgz|tar)$", "", os.path.basename(source)) or "pack"
            pack = os.path.join(staging_base, name)
            os.makedirs(pack, exist_ok=True)
            with tarfile.open(source, "r:*") as tf:
                for m in tf.getmembers():
                    parts = m.name.replace("\\", "/").split("/")
                    if m.name.startswith("/") or ".." in parts:
                        raise ValueError(f"tar 包含非法路径: {m.name}")
                tf.extractall(pack)
            return pack
        if source.startswith(("http://", "https://", "git://", "git@")) or source.endswith(".git"):
            name = os.path.basename(source.rstrip("/")).replace(".git", "") or "repo"
            pack = os.path.join(staging_base, name)
            subprocess.run(["git", "clone", "--depth=1", source, pack],
                           check=True, capture_output=True)
            return pack
        raise ValueError(f"不支持的安装来源: {source}")
    except Exception:
        shutil.rmtree(staging_base, ignore_errors=True)
        raise


# ── ECDSA 签名校验 ─────────────────────────────────────────────────────

def _pack_digest(pack_dir: str) -> bytes:
    """对 pack 内 *.md/*.json（排除 signature.json）按相对路径排序，
    逐个取 SHA-256 摘要拼接后再次 SHA-256，得到待签摘要。"""
    files = []
    for root, _dirs, fns in os.walk(pack_dir):
        for fn in fns:
            if not (fn.endswith(".md") or fn.endswith(".json")):
                continue
            p = os.path.join(root, fn)
            rel = os.path.relpath(p, pack_dir)
            if rel == _SIGNATURE_FILE:
                continue
            files.append(rel)
    files.sort()
    h = hashlib.sha256()
    for rel in files:
        with open(os.path.join(pack_dir, rel), "rb") as f:
            h.update(hashlib.sha256(f.read()).digest())
    return h.digest()


def verify_signature(pack_dir: str) -> str:
    """返回 verified / unsigned / failed 三态。

    signature.json 结构: {algo, sig: <base64 ASN.1 DER ECDSA-SHA256>, pub_key: <PEM>}
    """
    sig_path = os.path.join(pack_dir, _SIGNATURE_FILE)
    if not os.path.isfile(sig_path):
        return "unsigned"
    try:
        with open(sig_path, "r", encoding="utf-8") as f:
            data = json.load(f)
        sig = base64.b64decode(data["sig"])
        pub = serialization.load_pem_public_key(data["pub_key"].encode())
        if not isinstance(pub, ec.EllipticCurvePublicKey):
            return "failed"
        digest = _pack_digest(pack_dir)
        pub.verify(sig, digest, ec.ECDSA(utils.Prehashed(hashes.SHA256())))
        return "verified"
    except Exception:
        return "failed"


# ── 已安装记录 (SQLite: AIOPS_DATA_DIR/market.db) ───────────────────────

def _market_db_path() -> str:
    data_dir = os.environ.get("AIOPS_DATA_DIR", "/data")
    return os.path.join(data_dir, "market.db")


def _db_conn() -> sqlite3.Connection:
    db_path = _market_db_path()
    os.makedirs(os.path.dirname(db_path), exist_ok=True)
    conn = sqlite3.connect(db_path)
    conn.execute(
        "CREATE TABLE IF NOT EXISTS installed_packs ("
        " pack_id TEXT PRIMARY KEY, source TEXT, signature_state TEXT, installed_at TEXT)")
    return conn


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _record_installed(pack_id: str, source: str, state: str) -> None:
    conn = _db_conn()
    try:
        conn.execute(
            "INSERT OR REPLACE INTO installed_packs (pack_id, source, signature_state, installed_at)"
            " VALUES (?,?,?,?)", (pack_id, source, state, _now()))
        conn.commit()
    finally:
        conn.close()


def list_installed() -> list:
    conn = _db_conn()
    try:
        rows = conn.execute(
            "SELECT pack_id, source, signature_state, installed_at"
            " FROM installed_packs ORDER BY installed_at").fetchall()
        return [{"pack_id": r[0], "source": r[1], "signature_state": r[2], "installed_at": r[3]}
                for r in rows]
    finally:
        conn.close()


# ── 热重载 ─────────────────────────────────────────────────────────────

def _reload_registries() -> None:
    from skill_registry import SkillRegistry
    SkillRegistry.reload()


# ── 安装 / 卸载 ────────────────────────────────────────────────────────

def install(source: str, as_admin: bool = False) -> dict:
    """从 local 目录 / tarball / git 安装 skill pack 到用户 skills 目录。

    流程: staging → 逃逸检查 → 唯一性 → 签名校验(三态+REQUIRE_SIGNED 门控) → 落盘 → 记库 → reload。
    """
    if not as_admin:
        raise PermissionError("仅管理员可安装")

    staging = _fetch_to_staging(source)
    try:
        skills = _scan_skills(staging)
        if not skills:
            raise ValueError("未找到 SKILL.md")

        # 路径逃逸检查: 每个 SKILL.md 的 realpath 必须在 staging 内
        for s in skills:
            if not _is_within(staging, s):
                raise ValueError(f"路径逃逸: {s}")

        state = verify_signature(staging)
        if state == "failed":
            raise ValueError("签名校验失败")
        required = os.environ.get("MARKETPLACE_REQUIRE_SIGNED", "0") == "1"
        if required and state != "verified":
            raise ValueError("市场要求签名包")

        # 唯一性
        pack_id = os.path.basename(staging.rstrip("/"))
        dest = os.path.join(user_skills_dir(), pack_id)
        if os.path.exists(dest):
            raise ValueError(f"pack 已安装: {pack_id}")

        os.makedirs(os.path.dirname(dest), exist_ok=True)
        os.rename(staging, dest)
        _record_installed(pack_id, source, state)
        _reload_registries()
        return {"pack_id": pack_id, "signature_state": state,
                "skills": [os.path.basename(s) for s in skills]}
    finally:
        parent = os.path.dirname(staging)
        if parent and os.path.exists(parent):
            shutil.rmtree(parent, ignore_errors=True)


def uninstall(pack_id: str) -> dict:
    """卸载已安装 pack：删除目录 + 移除记录 + 热重载。"""
    base = os.path.realpath(user_skills_dir())
    dest = os.path.join(base, pack_id)
    real = os.path.realpath(dest)
    if not _is_within(base, real) or not os.path.isdir(real):
        raise ValueError(f"pack 未安装: {pack_id}")

    shutil.rmtree(real)
    conn = _db_conn()
    try:
        conn.execute("DELETE FROM installed_packs WHERE pack_id=?", (pack_id,))
        conn.commit()
    finally:
        conn.close()
    _reload_registries()
    return {"pack_id": pack_id, "uninstalled": True}
