"""Canonical graph contracts used by the orchestrator.

The package deliberately contains builders and contract validation only.  Graph
reads and writes still cross the query-api internal boundary.
"""

from .identity import (
    ASSET_NAMESPACE,
    GLOBAL_CLUSTER_SCOPE_ID,
    GRAPH_IDENTITY_VERSION,
    GRAPH_MUTATION_NAMESPACE,
    canonical_service_uid,
    edge_uid,
    entity_uid,
    hardware_component_uid,
    k8s_entity_uid,
    kubevirt_entity_uid,
    name_key_v1,
    physical_server_uid,
    provisional_service_uid,
    sha256_parts,
)
from .models import Edge, Entity, GraphMutation, GraphMutationBatch
from .ontology import validate_entity_type, validate_relation

__all__ = [
    "ASSET_NAMESPACE", "GLOBAL_CLUSTER_SCOPE_ID", "GRAPH_IDENTITY_VERSION",
    "GRAPH_MUTATION_NAMESPACE", "canonical_service_uid", "edge_uid",
    "entity_uid", "hardware_component_uid", "k8s_entity_uid",
    "kubevirt_entity_uid", "name_key_v1", "physical_server_uid",
    "provisional_service_uid", "sha256_parts", "Entity", "Edge",
    "GraphMutation", "GraphMutationBatch", "validate_entity_type",
    "validate_relation",
]
