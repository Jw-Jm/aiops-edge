import json

from control_plane_client import ControlPlaneClient


def test_knowledge_graph_client_binds_cluster_scope():
    calls = []

    def http(path, *, context_claims, method, data=None, headers=None):
        calls.append({
            "path": path,
            "claims": context_claims,
            "method": method,
            "body": json.loads(data.decode()),
        })
        return 200, b'{"id":42,"created":true}'

    result = ControlPlaneClient(http=http).knowledge_graph(
        "upsert_node",
        {"cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
         "type": "service", "name": "orders", "props": {"cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f"}},
        write=True,
    )

    assert result["id"] == 42
    assert calls[0]["path"] == "/internal/v1/control-plane/knowledge-graph"
    assert calls[0]["claims"]["scope_kind"] == "cluster"
    assert calls[0]["claims"]["cluster_id"] == "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
    assert calls[0]["body"]["operation"] == "upsert_node"
