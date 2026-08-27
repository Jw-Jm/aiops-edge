"""Orchestrator graph client: all graph traffic goes through InternalQueryClient."""
from __future__ import annotations
from typing import Any, Mapping


class GraphClient:
    def __init__(self, internal_query_client: Any, *, tenant_id: str, cluster_id: str, context_ref: str = ""):
        self.client = internal_query_client
        self.tenant_id, self.cluster_id, self.context_ref = tenant_id, cluster_id, context_ref

    def query(self, *, graph_operation: str, **params: Any) -> dict[str, Any]:
        return self.client.query_graph_v1(tenant_id=self.tenant_id, cluster_id=self.cluster_id,
                                          params={"graph_operation": graph_operation, **params},
                                          context_ref=self.context_ref).body

    def __call__(self, **params: Any) -> dict[str, Any]:
        operation = str(params.pop("graph_operation", ""))
        return self.query(graph_operation=operation, **params)
