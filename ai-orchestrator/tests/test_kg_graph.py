import kg_graph


class _FakeKnowledgeGraphClient:
    def __init__(self):
        self.calls = []

    def knowledge_graph(self, operation, body, *, write=False):
        self.calls.append((operation, body, write))
        if operation == "upsert_node":
            return {"id": 42, "created": True}
        return {"nodes": [], "edges": []}


def test_kg_node_upsert_uses_query_api_control_plane(monkeypatch):
    fake = _FakeKnowledgeGraphClient()
    monkeypatch.setattr(kg_graph, "_control_plane_factory", lambda: fake)

    node_id = kg_graph.upsert_node("service", "orders", {"cluster_id": "cluster-1"})

    assert node_id == 42
    assert fake.calls == [(
        "upsert_node",
        {"type": "service", "name": "orders", "cluster_id": "cluster-1",
         "props": {"cluster_id": "cluster-1", "created_by": "auto"}},
        True,
    )]


def test_pod_to_service_name_deployment():
    # deployment 型：候选含去最后两段 -hash-random
    assert "query-api" in kg_graph._pod_to_service_name("query-api-7966f8dbb8-sjswt")
    assert "orders" in kg_graph._pod_to_service_name("orders-5d9c8f6b4c-abc12")


def test_pod_to_service_name_statefulset():
    # statefulset 型：候选含去最后一段 -序号（含 2 段与 3 段两种形态）
    assert "mysql" in kg_graph._pod_to_service_name("mysql-0")
    assert "redis" in kg_graph._pod_to_service_name("redis-2")
    assert "deepflow-clickhouse" in kg_graph._pod_to_service_name("deepflow-clickhouse-0")


def test_pod_to_service_name_daemonset():
    # daemonset 型：候选含去最后一段 -hash
    assert "deepflow-agent" in kg_graph._pod_to_service_name("deepflow-agent-b88wd")


def test_pod_to_service_name_priority():
    # deployment 候选优先（去两段在前）
    assert kg_graph._pod_to_service_name("query-api-7966f8dbb8-sjswt")[0] == "query-api"


def test_pod_to_service_name_unparseable():
    # 无法推导：无 '-' 分隔
    assert kg_graph._pod_to_service_name("plainpod") == []
    assert kg_graph._pod_to_service_name("") == []
    assert kg_graph._pod_to_service_name(None) == []
