from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from kg.models import GraphMutation, GraphMutationBatch
from kg.runtime import GraphSyncRuntime, MAX_BATCH_MUTATIONS
from kg.builders.catalog import CatalogBuilder
from kg.builders.kubernetes import KubernetesBuilder
from kg.builders.kubevirt import KubeVirtBuilder


class FakeQuery:
    def __init__(self, payloads):
        self.payloads = payloads
        self.calls = []

    def query(self, **kwargs):
        self.calls.append(kwargs)
        return type("Result", (), {"body": self.payloads[kwargs["operation"]]})()


class FakeControlPlane:
    def __init__(self, *, fail_batch=False):
        self.calls = []
        self.fail_batch = fail_batch
        self._generation = 7

    def knowledge_graph(self, operation, body, *, write=False, tenant_id=None):
        self.calls.append((operation, body, write, tenant_id))
        if operation == "reconcile_scope" and body.get("phase") == "start":
            self._generation += 1
            return {"acquired": True, "reconcile_run_id": "run-1", "generation": self._generation}
        if operation == "batch_mutate" and self.fail_batch:
            raise RuntimeError("hugegraph unavailable")
        return {"accepted": True}


def _envelope(data, quality="complete"):
    return {"quality": quality, "data": data}


def test_runtime_reads_canonical_sources_in_fixed_order_and_projects_batches():
    query = FakeQuery({
        "catalog": _envelope({"services": [{"id": "svc-1", "name": "orders"}]}),
        "hardware_inventory": _envelope({"servers": [{
            "system_uuid": "host-1", "vendor": "Acme", "serial": "s-1", "hostname": "node-1"
        }]}),
        "kubernetes": _envelope({"pods": [{
            "kind": "Pod", "metadata": {"uid": "pod-1", "name": "orders-0"}
        }]}),
        "kubevirt": _envelope({"virtual_machines": [{
            "kind": "VirtualMachine", "metadata": {"uid": "vm-1", "name": "orders-vm"}
        }]}),
        "middleware": _envelope({"dependencies": [{"uid": "db-1", "name": "mysql"}]}),
        "topology": _envelope({"edges": [{"source": "orders", "target": "mysql", "calls": 1}]}),
        "network_topology": _envelope({"items": [{"entity_type": "nad", "uid": "nad-1", "name": "default"}]}),
        "changes": _envelope({"changes": [{"change_id": "chg-1", "summary": "rollout"}]}),
        "hardware_health": _envelope({"items": []}),
    })
    control = FakeControlPlane()
    runtime = GraphSyncRuntime(query_client=query, control_plane=control,
                               tenant_id="tenant-1", cluster_id="cluster-1")

    results = runtime.run_all_once()

    assert [result.source for result in results] == [
        "catalog", "hardware", "kubernetes", "kubevirt", "middleware", "trace", "change", "network"
    ]
    assert all(result.status == "success" for result in results)
    assert [call[0] for call in control.calls if call[0] == "batch_mutate"]
    assert [call["operation"] for call in query.calls] == [
        "catalog", "hardware_inventory", "hardware_health", "kubernetes", "kubevirt",
        "middleware", "topology", "changes", "network_topology"
    ]


def test_runtime_never_marks_stale_after_source_query_failure_or_partial_data():
    query = FakeQuery({
        "catalog": _envelope({"services": [{"id": "svc-1", "name": "orders"}]}, quality="partial"),
    })
    control = FakeControlPlane()
    runtime = GraphSyncRuntime(query_client=query, control_plane=control,
                               tenant_id="tenant-1", cluster_id="cluster-1")

    result = runtime.run_once("catalog")

    assert result.status == "failed"
    assert not any(call[0] == "batch_mutate" for call in control.calls)
    assert not any(call[0] == "mark_stale_generation" for call in control.calls)
    assert any(call[0] == "reconcile_scope" and call[1].get("phase") == "failed" for call in control.calls)


def test_runtime_treats_empty_complete_source_as_no_data_without_stale_cleanup():
    query = FakeQuery({"catalog": _envelope({"services": [], "applications": [], "businesses": []})})
    control = FakeControlPlane()
    runtime = GraphSyncRuntime(query_client=query, control_plane=control,
                               tenant_id="tenant-1", cluster_id="cluster-1")

    result = runtime.run_once("catalog")

    assert result.status == "no_data"
    assert result.mutations == 0
    assert not any(call[0] == "batch_mutate" for call in control.calls)
    assert not any(call[0] == "mark_stale_generation" for call in control.calls)
    assert any(call[0] == "reconcile_scope" and call[1].get("phase") == "no_data" for call in control.calls)


def test_runtime_splits_mutations_at_documented_batch_limit():
    class LargeBuilder:
        def __init__(self, tenant_id, generation=1):
            self.tenant_id, self.cluster_id, self.generation = tenant_id, "", generation

        def build(self, records):
            mutations = tuple(
                GraphMutation(mutation_id=f"m-{i}", kind="upsert_vertex",
                              vertex={"entity_uid": f"e-{i}", "entity_type": "service",
                                      "tenant_id": self.tenant_id, "cluster_id": self.cluster_id,
                                      "name": f"svc-{i}"}, edge=None,
                              source="catalog", generation=self.generation)
                for i in range(MAX_BATCH_MUTATIONS + 1)
            )
            return GraphMutationBatch(self.tenant_id, self.cluster_id, "catalog",
                                      self.generation, 1, mutations)

    query = FakeQuery({"catalog": _envelope({"services": [{"id": "svc", "name": "orders"}]})})
    control = FakeControlPlane()
    runtime = GraphSyncRuntime(query_client=query, control_plane=control,
                               tenant_id="tenant-1", cluster_id="cluster-1",
                               builders={"catalog": LargeBuilder})

    result = runtime.run_once("catalog")

    batches = [call for call in control.calls if call[0] == "batch_mutate"]
    assert result.status == "success"
    assert [len(call[1]["mutations"]) for call in batches] == [MAX_BATCH_MUTATIONS, 1]
    assert any(call[0] == "mark_stale_generation" for call in control.calls)
    finish = [call for call in control.calls
              if call[0] == "reconcile_scope" and call[1].get("phase") == "success"][-1]
    assert finish[1]["vertices_seen"] == MAX_BATCH_MUTATIONS + 1
    assert finish[1]["edges_seen"] == 0


def test_builders_materialize_documented_cross_domain_edges():
    catalog = CatalogBuilder("tenant-1").build([
        {"entity_type": "business", "id": "b-1", "name": "Retail"},
        {"entity_type": "application", "id": "a-1", "name": "Orders", "business_id": "b-1"},
        {"entity_type": "service", "id": "s-1", "name": "orders", "application_id": "a-1"},
    ])
    assert {m.edge["relation_type"] for m in catalog.mutations if m.edge} == {"BELONGS_TO"}

    kubernetes = KubernetesBuilder("tenant-1", "cluster-1").build([
        {"kind": "Cluster", "metadata": {"uid": "cluster-kube-system", "name": "cluster-1"}},
        {"kind": "Namespace", "metadata": {"uid": "ns-1", "name": "obs"}},
        {"kind": "Node", "metadata": {"uid": "node-1", "name": "worker-1"}},
        {"kind": "Pod", "metadata": {"uid": "pod-1", "name": "orders", "namespace": "obs"},
         "spec": {"nodeName": "worker-1", "volumes": [{"persistentVolumeClaim": {"claimName": "data"}}]},
         "status": {"podIP": "10.0.0.7"},
         },
        {"kind": "Service", "metadata": {"uid": "svc-1", "name": "orders", "namespace": "obs"},
         "spec": {"selector": {"app": "orders"}}},
        {"kind": "EndpointSlice", "metadata": {"uid": "ep-1", "name": "orders-1", "namespace": "obs",
                                                      "labels": {"kubernetes.io/service-name": "orders"}},
         "endpoints": [{"addresses": ["10.0.0.7"]}]},
        {"kind": "PersistentVolumeClaim", "metadata": {"uid": "pvc-1", "name": "data", "namespace": "obs"},
         "spec": {"volumeName": "pv-1"}},
        {"kind": "PersistentVolume", "metadata": {"uid": "pv-1", "name": "pv-1"}},
    ])
    assert {m.edge["relation_type"] for m in kubernetes.mutations if m.edge} >= {
        "CONTAINS", "RUNS_ON", "TARGETS", "BACKED_BY", "USES_VOLUME", "BOUND_TO"
    }

    kubevirt = KubeVirtBuilder("tenant-1", "cluster-1").build([
        {"kind": "Node", "metadata": {"uid": "node-1", "name": "worker-1"}},
        {"kind": "VirtualMachine", "metadata": {"uid": "vm-1", "name": "orders-vm", "namespace": "obs"},
         "spec": {"template": {"spec": {"volumes": [{"persistentVolumeClaim": {"claimName": "data"}}]}}}},
        {"kind": "VirtualMachineInstance", "metadata": {"uid": "vmi-1", "name": "orders-vm", "namespace": "obs",
                                                               "ownerReferences": [{"uid": "vm-1"}]},
         "status": {"nodeName": "worker-1"}},
        {"kind": "Pod", "metadata": {"uid": "launcher-1", "name": "virt-launcher", "namespace": "obs",
                                         "labels": {"kubevirt.io/domain": "orders-vm"}}},
        {"kind": "PersistentVolumeClaim", "metadata": {"uid": "pvc-1", "name": "data", "namespace": "obs"}},
    ])
    assert {m.edge["relation_type"] for m in kubevirt.mutations if m.edge} >= {
        "INSTANCE_OF", "RUNS_ON", "BELONGS_TO", "USES_VOLUME"
    }
