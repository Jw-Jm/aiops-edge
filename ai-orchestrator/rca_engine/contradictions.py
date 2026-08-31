from __future__ import annotations

from typing import Any, Iterable

from .scorer import parse_timestamp


def evaluate_contradictions(candidate: dict[str, Any], evidences: Iterable[dict[str, Any]], *,
                            symptom_time: Any = None, window_end: Any = None) -> list[dict[str, Any]]:
    """Apply deterministic, explainable contradiction rules.

    Providers may add richer signals, but the engine only trusts explicit
    V2 fields and frozen timestamps.  A contradiction is evidence for
    downgrading a conclusion, never a reason to silently discard source data.
    """
    uid = str(candidate.get("entity_uid") or "")
    result: list[dict[str, Any]] = []
    symptom = parse_timestamp(symptom_time)
    end = parse_timestamp(window_end) if window_end is not None else None
    for evidence in evidences:
        if str(evidence.get("entity_uid") or "") != uid:
            continue
        code = str(evidence.get("contradiction_code") or "").strip()
        if evidence.get("contradicts") is True or code:
            result.append({
                "code": code or "PROVIDER_CONTRADICTION",
                "entity_uid": uid,
                "evidence_id": str(evidence.get("evidence_id") or ""),
                "severity": "blocking",
                "reason": str(evidence.get("contradiction_reason") or "explicit source contradiction"),
            })
            continue
        observed = next((parse_timestamp(evidence.get(key)) for key in
                         ("observed_at", "timestamp", "event_time", "detected_at")
                         if evidence.get(key) is not None), None)
        if observed is not None and symptom is not None and observed > symptom:
            # A change/observation after the frozen symptom cannot explain it.
            if evidence.get("category") == "change":
                result.append({"code": "CHANGE_AFTER_SYMPTOM", "entity_uid": uid,
                               "evidence_id": str(evidence.get("evidence_id") or ""),
                               "severity": "blocking", "reason": "change observed after symptom_time"})
        if observed is not None and end is not None and observed > end:
            result.append({"code": "EVIDENCE_OUTSIDE_WINDOW", "entity_uid": uid,
                           "evidence_id": str(evidence.get("evidence_id") or ""),
                           "severity": "blocking", "reason": "evidence observed after frozen window_end"})
    return result
