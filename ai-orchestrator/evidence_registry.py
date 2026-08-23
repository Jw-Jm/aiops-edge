"""Evidence Registry — Run→Evidence 内存注册表（tenant+cluster+run 三元授权）。

MVP 范围（诚实声明）：内存态，进程重启即失；持久化属后续 Gate。
只读语义：本模块只登记/查询证据条目，不执行、不变更证据内容。

设计：
- register_run(run_id, tenant_id, cluster_id, evidences)：登记一次 RCA 产出的证据链；
  每个条目补齐稳定 evidence_id（缺失时用 sha256(json.dumps(entry, sort_keys=True))[:32]），
  scope (tenant_id/cluster_id) 单独存储，不混入公开条目。
- authorize_and_get(run_id, evidence_id, tenant_id, cluster_id)：
  run/evidence 未知 → LookupError；scope 不匹配 → PermissionError（fail-closed）。
"""
from __future__ import annotations

import copy
import hashlib
import json
import threading
from typing import Any, Dict, List, Optional, Tuple


def _generate_evidence_id(entry: Dict[str, Any]) -> str:
    """确定性 evidence_id：相同内容重复注册得到相同 id。"""
    payload = json.dumps(entry, sort_keys=True, ensure_ascii=False, default=str)
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()[:32]


class EvidenceRegistry:
    """线程安全的内存 Evidence 注册表（单例经 get_registry() 访问）。"""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        # run_id -> {"scope": (tenant_id, cluster_id), "entries": {evidence_id: public_entry}}
        self._runs: Dict[str, Dict[str, Any]] = {}

    def register_run(
        self,
        run_id: str,
        tenant_id: str,
        cluster_id: str,
        evidences: List[Dict[str, Any]],
    ) -> List[str]:
        """登记一个 Run 的证据链（幂等：同内容重复注册得到相同 evidence_id）。"""
        run_key = str(run_id)
        scope: Tuple[str, str] = (str(tenant_id), str(cluster_id))
        entries: Dict[str, Dict[str, Any]] = {}
        for raw in evidences or []:
            if not isinstance(raw, dict):
                continue
            entry = dict(raw)
            eid = str(entry.get("evidence_id") or "") or _generate_evidence_id(entry)
            entry["evidence_id"] = eid
            entries[eid] = entry
        with self._lock:
            self._runs[run_key] = {"scope": scope, "entries": entries}
        return list(entries.keys())

    def list_evidences(self, run_id: str) -> List[Dict[str, Any]]:
        """列举 Run 的公开证据条目（不含内部 scope 字段）。"""
        with self._lock:
            rec = self._runs.get(str(run_id))
            if rec is None:
                return []
            return [copy.deepcopy(e) for e in rec["entries"].values()]

    def get_evidence(self, run_id: str, evidence_id: str) -> Optional[Dict[str, Any]]:
        """无 scope 校验的内部读取（仅供服务端内部使用）。"""
        with self._lock:
            rec = self._runs.get(str(run_id))
            if rec is None:
                return None
            entry = rec["entries"].get(str(evidence_id))
            return copy.deepcopy(entry) if entry else None

    def authorize_and_get(
        self,
        run_id: str,
        evidence_id: str,
        tenant_id: str,
        cluster_id: str,
    ) -> Dict[str, Any]:
        """三元授权读取：run+evidence 必须存在，且 tenant/cluster 必须与注册 scope 一致。"""
        with self._lock:
            rec = self._runs.get(str(run_id))
            if rec is None:
                raise LookupError(f"run 不存在: {run_id}")
            entry = rec["entries"].get(str(evidence_id))
            if entry is None:
                raise LookupError(f"evidence 不存在: {evidence_id}")
            scope_tenant, scope_cluster = rec["scope"]
            if str(tenant_id) != scope_tenant or str(cluster_id) != scope_cluster:
                raise PermissionError("SCOPE_MISMATCH")
            return copy.deepcopy(entry)


# 模块级单例（可注入：测试直接替换 evidence_registry._registry）
_registry = EvidenceRegistry()


def get_registry() -> EvidenceRegistry:
    return _registry
