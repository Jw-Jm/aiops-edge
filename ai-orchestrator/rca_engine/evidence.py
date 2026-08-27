from __future__ import annotations
from collections import defaultdict
from typing import Any, Iterable

EVIDENCE_CATEGORIES = {"metric", "trace", "log", "alert", "change", "kubernetes_event", "kubevirt_event",
                       "hardware_sensor", "hardware_sel", "inventory", "graph_relation"}


def normalize_evidence(items: Iterable[dict[str, Any]]) -> list[dict[str, Any]]:
    result = []
    for item in items:
        if not isinstance(item, dict):
            continue
        category = str(item.get("category") or item.get("evidence_type") or "").strip().lower()
        if category in {"metric_anomaly", "capacity_anomaly"}: category = "metric"
        if category in {"trace_anomaly"}: category = "trace"
        if category in {"log_error", "log_pattern"}: category = "log"
        if category in {"k8s_state", "k8s_event"}: category = "kubernetes_event"
        if category == "hardware_event": category = "hardware_sensor"
        copy = dict(item); copy["category"] = category
        result.append(copy)
    return result


def evidence_for_candidate(items: Iterable[dict[str, Any]], entity_uid: str) -> list[dict[str, Any]]:
    return [item for item in normalize_evidence(items)
            if not item.get("entity_uid") or str(item.get("entity_uid")) == entity_uid]


def independent_categories(items: Iterable[dict[str, Any]]) -> int:
    return len({str(item.get("category")) for item in items if item.get("category") in EVIDENCE_CATEGORIES})


def group_by_entity(items: Iterable[dict[str, Any]]) -> dict[str, list[dict[str, Any]]]:
    grouped = defaultdict(list)
    for item in normalize_evidence(items):
        grouped[str(item.get("entity_uid") or "")].append(item)
    return dict(grouped)
