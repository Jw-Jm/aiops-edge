from types import SimpleNamespace

from rca_engine.entity_resolver import resolve_entity


def test_resolve_entity_falls_back_to_non_service_ontology_match():
    calls = []

    def graph_client(**params):
        calls.append(params)
        if params["entity_type"] == "service":
            return {"items": []}
        return {"items": [{"entity_uid": "node-uid", "entity_type": "k8s_node", "name": "orbstack"}]}

    request = SimpleNamespace(entity_uid="", resource_id="", entity_name="orbstack")
    entity = resolve_entity(request, graph_client)

    assert entity["entity_uid"] == "node-uid"
    assert [call["entity_type"] for call in calls] == ["service", ""]


def test_resolve_entity_keeps_ambiguous_non_service_match_fail_closed():
    def graph_client(**params):
        return {"items": [] if params["entity_type"] == "service" else [
            {"entity_uid": "node-a"}, {"entity_uid": "node-b"},
        ]}

    request = SimpleNamespace(entity_uid="", resource_id="", entity_name="shared-name")
    assert resolve_entity(request, graph_client) is None


def test_resolve_entity_continues_when_service_lookup_reports_not_found():
    calls = []

    def graph_client(**params):
        calls.append(params)
        if params["entity_type"] == "service":
            raise RuntimeError("GRAPH_ENTITY_NOT_FOUND: orbstack")
        return {"entity": {"entity_uid": "node-uid", "entity_type": "k8s_node", "name": "orbstack"}}

    request = SimpleNamespace(entity_uid="", resource_id="", entity_name="orbstack")
    entity = resolve_entity(request, graph_client)

    assert entity["entity_uid"] == "node-uid"
    assert [call["entity_type"] for call in calls] == ["service", ""]


def test_resolve_entity_uses_persisted_target_type_hint():
    calls = []

    def graph_client(**params):
        calls.append(params)
        return {"entity": {"entity_uid": "node-uid", "entity_type": "k8s_node", "name": "orbstack"}}

    request = SimpleNamespace(entity_uid="", resource_id="", entity_name="orbstack", target_type="node")
    entity = resolve_entity(request, graph_client)

    assert entity["entity_uid"] == "node-uid"
    assert [call["entity_type"] for call in calls] == ["k8s_node"]


def test_resolve_entity_does_not_probe_human_alias_as_vertex_uid():
    calls = []

    def graph_client(**params):
        calls.append(params)
        return {"entity": {"entity_uid": "node-uid", "entity_type": "k8s_node", "name": "orbstack"}}

    request = SimpleNamespace(entity_uid="", resource_id="orbstack", entity_name="orbstack", target_type="node")
    entity = resolve_entity(request, graph_client)

    assert entity["entity_uid"] == "node-uid"
    assert [call["graph_operation"] for call in calls] == ["resolve_entity"]
