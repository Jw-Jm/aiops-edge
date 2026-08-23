"""EX.6 Policy Real SoT Provider — V9.3 Execution Infrastructure。

将 Policy Context 的权威来源（authorization_sot / cluster_state）接入真实数据源模型：
- authorization_sot ← MySQL（V9.3 Authorization SoT）
- cluster_state     ← 经 query-api 只读（orchestrator 不直连集群，无旁路）

本模块为内存 mock provider（真实 MySQL/query-api 接入属后续），但确立无旁路边界：
orchestrator 不持有 kubectl / 不直连集群 / 不绕过 query-api。
"""
from __future__ import annotations

from typing import Any, Dict


class AuthorizationSoTProvider:
    """MySQL Authorization SoT（内存 mock）。V9.3 冻结 Authorization SoT = MySQL。"""

    def __init__(self, sot_data: Dict[str, Dict[str, Any]]) -> None:
        self._sot = dict(sot_data)

    def load_authorization(self, cluster_id: str) -> Dict[str, Any]:
        """从 MySQL SoT 读取该 cluster 的授权信息。"""
        data = self._sot.get(cluster_id)
        if data is None:
            return {"enabled": False, "capabilities": []}
        return dict(data)


class ClusterStateProvider:
    """经 query-api 只读获取集群状态（内存 mock）。orchestrator 不直连集群。"""

    def __init__(self, state_data: Dict[str, Dict[str, Any]]) -> None:
        self._state = dict(state_data)

    def load_cluster_state(self, cluster_id: str) -> Dict[str, Any]:
        """经 query-api 只读获取集群状态（无旁路：无 kubectl/直连）。"""
        data = self._state.get(cluster_id)
        if data is None:
            return {"healthy": False, "impact_pods": 0}
        return dict(data)
