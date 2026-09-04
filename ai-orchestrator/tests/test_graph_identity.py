import json
from pathlib import Path

from kg.identity import (canonical_service_uid, edge_uid, hardware_component_uid, k8s_entity_uid,
                         kubevirt_entity_uid, name_key_v1, provisional_service_uid, sha256_parts)
from kg.models import GraphMutation, GraphMutationBatch


def test_identity_fixture_is_cross_language_stable():
    fixture = json.loads((Path(__file__).parents[2] / "tests/fixtures/graph/graph_identity_v1.json").read_text())
    for vector in fixture["vectors"]:
        assert sha256_parts(*vector["parts"]) == vector["expected"]
    for entity in fixture["entities"]:
        kind, parts = entity["kind"], entity["parts"]
        assert f"{kind}:v1:{':'.join(parts)}" == entity["expected"]


def test_uid_constructors_and_deterministic_mutation():
    assert k8s_entity_uid("Pod", "cluster-1", "pod-1") == "k8s-pod:v1:cluster-1:pod-1"
    assert kubevirt_entity_uid("VM", "cluster-1", "vm-1") == "kubevirt-vm:v1:cluster-1:vm-1"
    assert provisional_service_uid("tenant-1", "cluster-1", "Order Service")
    assert canonical_service_uid("tenant-1", "service-1") == "service:v1:tenant-1:service-1"
    assert hardware_component_uid("asset-1", "DIMM", "DIMM_A1").startswith("component:v1:asset-1:dimm:")
    assert edge_uid("tenant-1", "DEPENDS_ON", "a", "b")
    assert name_key_v1("  Order\t Service ") == "order service"


def test_mutation_batch_is_typed_and_idempotent():
    from kg.builders.base import GraphBuilder
    builder = GraphBuilder("fixture", "tenant-1", "cluster-1", generation=4, attrs_version=2)
    entity = builder.entity(uid="service:v1:tenant-1:service-1", entity_type="service", name="order-service")
    first = GraphMutation.upsert_vertex(entity, source="fixture", generation=4)
    second = GraphMutation.upsert_vertex(entity, source="fixture", generation=4)
    assert first.mutation_id == second.mutation_id
    batch = GraphMutationBatch("tenant-1", "cluster-1", "fixture", 4, 2, (first,))
    assert batch.to_dict()["vertices"][0]["entity_uid"] == entity.entity_uid
