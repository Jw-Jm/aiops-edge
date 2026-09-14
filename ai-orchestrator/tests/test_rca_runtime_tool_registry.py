"""D15 回归：Investigation worker 的内部查询客户端必须先初始化工具注册表。

真实环境验证发现：只有 orchestrator 主应用与 kg.runtime 会调用
``init_default_tool_registry``；rca_engine.runtime 的客户端在 worker 中构造时
注册表为空，``query_graph.v1`` 解析失败 → invalid_context → GRAPH_UNAVAILABLE
→ 所有运行 0 证据收场（rca.v2 直接 insufficient_evidence）。
"""
from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import os

from internal_query_client import InternalQueryClient
from tool_registry import ToolRegistry, init_default_tool_registry
import rca_engine.runtime as rca_runtime


@pytest.fixture()
def isolated_registry():
    """清空注册表以模拟 worker 冷启动，测试结束后恢复。"""
    saved = dict(ToolRegistry._tools)
    ToolRegistry._tools.clear()
    yield ToolRegistry
    ToolRegistry._tools.clear()
    ToolRegistry._tools.update(saved)


def test_client_factory_initializes_tool_registry(isolated_registry, monkeypatch):
    # 冷启动：注册表为空时，query_graph.v1 不可解析（get 返回 None）
    assert ToolRegistry.get("query_graph.v1") is None

    # 私钥来自环境变量（worker 已注入）；工厂必须先补齐注册表再构造客户端
    monkeypatch.setenv(
        "TRUSTED_CONTEXT_PRIVATE_KEY",
        os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY", "") or _any_ed25519_key(),
    )
    client = rca_runtime._client()
    assert isinstance(client, InternalQueryClient)
    tool = ToolRegistry.get("query_graph.v1")
    assert tool is not None, "client factory must initialize the default tool registry"
    assert tool.lifecycle_status == "active"
    assert tool.execution_state != "disabled"


def test_client_factory_is_idempotent(isolated_registry, monkeypatch):
    monkeypatch.setenv(
        "TRUSTED_CONTEXT_PRIVATE_KEY",
        os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY", "") or _any_ed25519_key(),
    )
    rca_runtime._client()
    first = sorted(t.tool_id for t in ToolRegistry.list_all())
    rca_runtime._client()
    assert sorted(t.tool_id for t in ToolRegistry.list_all()) == first


def _any_ed25519_key() -> str:
    """生成可被 _load_private_key 接受的测试私钥（base64url 的 Go 64 字节 seed+public）。"""
    import base64

    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

    key = Ed25519PrivateKey.generate()
    seed = key.private_bytes_raw()
    public = key.public_key().public_bytes_raw()
    return base64.urlsafe_b64encode(seed + public).decode().rstrip("=")
