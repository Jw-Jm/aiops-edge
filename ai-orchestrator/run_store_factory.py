"""Run 持久化启动强制化（P0）：生产模式禁止静默退回内存。"""
from __future__ import annotations

import os
import socket
from urllib.parse import urlsplit


class PersistenceConfigError(RuntimeError):
    """生产模式缺少远端持久化必需环境变量。"""

    def __init__(self, missing_items: list[str]):
        self.missing_items = list(missing_items)
        super().__init__(f"缺少 Run 持久化必需环境变量: {', '.join(missing_items)}")


def resolve_deployment_mode() -> str:
    """解析部署模式。

    AIOPS_DEPLOYMENT_MODE in {"production","development"} (default "development");
    also "production" if RUN_PERSISTENCE_REQUIRED in {"1","true","yes"}
    """
    mode = os.environ.get("AIOPS_DEPLOYMENT_MODE", "").strip().lower()
    if mode == "production":
        return "production"
    # RUN_PERSISTENCE_REQUIRED 兼容历史开关
    req = os.environ.get("RUN_PERSISTENCE_REQUIRED", "").strip().lower()
    if req in ("1", "true", "yes"):
        return "production"
    if mode == "development":
        return "development"
    # default
    return "development"


def required_persistence_env() -> dict[str, str | None]:
    return {
        "QUERY_API_URL": os.environ.get("QUERY_API_URL"),
        "TRUSTED_CONTEXT_PRIVATE_KEY": os.environ.get("TRUSTED_CONTEXT_PRIVATE_KEY"),
        "INTERNAL_TOKEN": os.environ.get("INTERNAL_TOKEN"),
    }


def check_control_plane_reachable(url: str, timeout: float = 3.0) -> None:
    """校验 control-plane 可达性；不可达则抛 ConnectionError。"""
    parsed = urlsplit(url)
    host = parsed.hostname
    if not host:
        raise ConnectionError(f"control-plane URL 无 host: {url}")
    scheme = (parsed.scheme or "http").lower()
    port = parsed.port
    if port is None:
        port = 443 if scheme == "https" else 80
    try:
        conn = socket.create_connection((host, port), timeout)
        conn.close()
    except OSError as e:
        raise ConnectionError(f"control-plane 不可达 {host}:{port}: {e}") from e


def is_ready(mode: str, backend: str) -> bool:
    """readyz 纯函数：生产+memory 视为未就绪。"""
    if mode == "production" and backend == "memory":
        return False
    return True


def build_run_state_store(fallback_store):
    """构建 RunStateStore，返回 (store, backend).

    backend in {"remote","memory"}
    """
    mode = resolve_deployment_mode()
    required = required_persistence_env()
    missing = [k for k, v in required.items() if not v]

    if mode == "production":
        if missing:
            raise PersistenceConfigError(missing)
        # 懒导入，避免循环依赖
        from run_cache import RunCache as _RunCache
        from control_plane_client import ControlPlaneClient as _ControlPlaneClient
        from persistent_run_repository import PersistentRunRepository as _PersistentRunRepository
        from persistent_run_state_store import PersistentRunStateStore as _PersistentRunStateStore

        # 先做可达性探测，失败直接抛 ConnectionError（启动失败）
        check_control_plane_reachable(os.environ["QUERY_API_URL"])
        _run_cache = _RunCache()
        _run_repository = _PersistentRunRepository(client=_ControlPlaneClient(), cache=_run_cache)
        store = _PersistentRunStateStore(repository=_run_repository, fallback=fallback_store)
        return store, "remote"
    else:
        if missing:
            print(f"[WARN] Run 持久化未配置({', '.join(missing)})，退化为内存存储（仅开发模式允许）", flush=True)
            return fallback_store, "memory"
        try:
            from run_cache import RunCache as _RunCache
            from control_plane_client import ControlPlaneClient as _ControlPlaneClient
            from persistent_run_repository import PersistentRunRepository as _PersistentRunRepository
            from persistent_run_state_store import PersistentRunStateStore as _PersistentRunStateStore

            _run_cache = _RunCache()
            _run_repository = _PersistentRunRepository(client=_ControlPlaneClient(), cache=_run_cache)
            # 开发模式也做可达性校验？ spec dev lenient: try build remote; on exception warn+fallback
            # 为保持 lenient，reachable 失败也应 fallback 而非 raise
            try:
                check_control_plane_reachable(os.environ["QUERY_API_URL"])
            except Exception as e:
                print(f"[WARN] Run 远端存储不可达({e})，退化为内存存储（开发模式）", flush=True)
                return fallback_store, "memory"
            store = _PersistentRunStateStore(repository=_run_repository, fallback=fallback_store)
            return store, "remote"
        except Exception as e:  # noqa: BLE001 — 开发模式保持 lenient
            print(f"[WARN] Run 远端存储初始化失败({e})，退化为内存存储（开发模式）", flush=True)
            return fallback_store, "memory"
