"""graph-identity-v1, kept byte-for-byte compatible with the Go package."""
from __future__ import annotations

import hashlib
import uuid
from typing import Iterable

GRAPH_IDENTITY_VERSION = "graph-identity-v1"
GRAPH_ONTOLOGY_VERSION = "graph-ontology-v2"
GRAPH_DTO_VERSION = "graph-dto-v1"
UNIT_SEPARATOR = "\x1f"
ASSET_NAMESPACE = uuid.UUID("0b8607dd-6b92-5e95-b007-d32874ffefab")
GRAPH_MUTATION_NAMESPACE = uuid.UUID("7af0bc4b-dba0-56b1-ac7c-0fe13db2ef5b")
GLOBAL_CLUSTER_SCOPE_ID = "00000000-0000-0000-0000-000000000000"


def name_key_v1(value: str) -> str:
    return " ".join((value or "").strip().lower().split())


def sha256_parts(*parts: str) -> str:
    return hashlib.sha256(UNIT_SEPARATOR.join(str(p) for p in parts).encode("utf-8")).hexdigest()


def entity_uid(kind: str, *parts: str) -> str:
    return f"{kind.strip()}:v1:{':'.join(str(p) for p in parts)}"


def edge_uid(tenant_id: str, relation_type: str, source_uid: str, target_uid: str) -> str:
    return "edge:v1:" + sha256_parts(tenant_id, relation_type, source_uid, target_uid)


def k8s_entity_uid(kind: str, cluster_id: str, object_uid: str) -> str:
    clean = kind.strip().lower()
    if clean.startswith("k8s-"):
        clean = clean[4:]
    return entity_uid("k8s-" + clean, cluster_id, object_uid)


def kubevirt_entity_uid(kind: str, cluster_id: str, object_uid: str) -> str:
    clean = kind.strip().lower()
    if clean.startswith("kubevirt-"):
        clean = clean[9:]
    return entity_uid("kubevirt-" + clean, cluster_id, object_uid)


def physical_server_uid(asset_uuid: str) -> str:
    return entity_uid("physical-server", asset_uuid)


def hardware_component_uid(asset_uuid: str, component_type: str, stable_locator: str) -> str:
    return entity_uid("component", asset_uuid, component_type.strip().lower(), sha256_parts(stable_locator))


def provisional_service_uid(tenant_id: str, cluster_id: str, service_name: str) -> str:
    return entity_uid("service-provisional", tenant_id, cluster_id, sha256_parts(name_key_v1(service_name)))


def canonical_service_uid(tenant_id: str, service_uuid: str) -> str:
    return entity_uid("service", tenant_id, service_uuid)


def asset_uuid_v5(tenant_id: str, system_uuid: str = "", vendor: str = "", serial: str = "") -> str | None:
    system_uuid, vendor, serial = system_uuid.strip(), vendor.strip(), serial.strip()
    if system_uuid:
        name = UNIT_SEPARATOR.join((tenant_id, system_uuid))
    elif vendor and serial:
        name = UNIT_SEPARATOR.join((tenant_id, vendor, serial))
    else:
        return None
    return str(uuid.uuid5(ASSET_NAMESPACE, name))


def mutation_uuid(kind: str, object_uid: str, attrs_version: int, generation: int) -> str:
    return str(uuid.uuid5(GRAPH_MUTATION_NAMESPACE, f"{kind}|{object_uid}|{attrs_version}|{generation}"))
