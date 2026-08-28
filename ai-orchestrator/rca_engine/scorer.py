from __future__ import annotations
from dataclasses import dataclass, asdict
from datetime import datetime, timezone
from typing import Any, Iterable

WEIGHTS = {"topology": .20, "temporal": .20, "anomaly": .15, "change": .10,
           "trace": .10, "hardware_severity": .15, "co_failure": .10}


@dataclass(frozen=True)
class ScoreBreakdown:
    topology: float = 0.0
    temporal: float = 0.0
    anomaly: float = 0.0
    change: float = 0.0
    trace: float = 0.0
    hardware_severity: float = 0.0
    co_failure: float = 0.0
    redundancy_penalty: float = 0.0

    @property
    def raw(self) -> float:
        return sum(WEIGHTS[key] * getattr(self, key) for key in WEIGHTS)

    @property
    def score(self) -> float:
        return max(0.0, min(1.0, self.raw - self.redundancy_penalty))

    def to_dict(self) -> dict[str, float]:
        return {**asdict(self), "raw": round(self.raw, 4), "score": round(self.score, 4)}


def topology_score(hops: int | None) -> float:
    return {1: 1.0, 2: .88, 3: .76, 4: .64, 5: .52, 6: .40}.get(int(hops or 0), 0.0)


def parse_timestamp(value: Any) -> datetime | None:
    if isinstance(value, datetime):
        parsed = value
    elif isinstance(value, str) and value.strip():
        try:
            parsed = datetime.fromisoformat(value.strip().replace("Z", "+00:00"))
        except ValueError:
            return None
    else:
        return None
    return parsed.replace(tzinfo=timezone.utc) if parsed.tzinfo is None else parsed.astimezone(timezone.utc)


def normalize_timestamp(value: Any, *, field: str) -> str:
    parsed = parse_timestamp(value)
    if parsed is None:
        raise ValueError(f"{field} must be an ISO-8601 timestamp")
    return parsed.isoformat().replace("+00:00", "Z")


def timestamp_text(value: Any) -> str:
    parsed = parse_timestamp(value)
    return parsed.isoformat().replace("+00:00", "Z") if parsed is not None else ""


def deterministic_temporal_score(evidences: Iterable[dict[str, Any]], symptom_time: Any) -> float:
    """Score evidence timing against the frozen symptom timestamp.

    Evidence providers may expose different timestamp names, but they cannot
    supply a pre-computed temporal score.  This keeps replay and audit output
    deterministic across providers.
    """
    symptom = parse_timestamp(symptom_time)
    if symptom is None:
        return 0.0
    scores: list[float] = []
    timestamp_keys = ("observed_at", "timestamp", "occurred_at", "event_time", "detected_at")
    for evidence in evidences:
        observed = next((parse_timestamp(evidence.get(key)) for key in timestamp_keys if evidence.get(key) is not None), None)
        if observed is None:
            continue
        delta_minutes = (symptom - observed).total_seconds() / 60.0
        if delta_minutes >= 0:
            scores.append(1.0 if delta_minutes <= 5 else .8 if delta_minutes <= 15 else .5 if delta_minutes <= 60 else 0.0)
        else:
            scores.append(.4 if -delta_minutes <= 2 else 0.0)
    return max(scores, default=0.0)


def score_candidate(candidate: dict[str, Any], evidences: list[dict[str, Any]], *, hops: int | None = None,
                    symptom_time: Any = None) -> ScoreBreakdown:
    categories = {str(e.get("category")) for e in evidences}
    anomaly = max((float(e.get("severity", e.get("score", 0.0)) or 0.0) for e in evidences
                  if e.get("category") in {"metric", "alert", "kubernetes_event", "hardware_sensor", "hardware_sel"}), default=0.0)
    anomaly = max(0.0, min(1.0, anomaly))
    hardware = max((float(e.get("severity", e.get("score", 0.0)) or 0.0) for e in evidences
                    if e.get("category") in {"hardware_sensor", "hardware_sel", "inventory"}), default=0.0)
    change = max((1.0 if e.get("same_entity") else .6 if e.get("same_scope") else .3
                  for e in evidences if e.get("category") == "change"), default=0.0)
    trace = max((1.0 if e.get("degraded") else .5 for e in evidences if e.get("category") == "trace"), default=0.0)
    temporal = deterministic_temporal_score(evidences, symptom_time)
    co_failure = max((float(e.get("co_failure_score", 0.0) or 0.0) for e in evidences), default=0.0)
    return ScoreBreakdown(topology=topology_score(hops), temporal=min(1.0, temporal), anomaly=anomaly,
                          change=change, trace=trace, hardware_severity=min(1.0, hardware),
                          co_failure=min(1.0, co_failure), redundancy_penalty=float(candidate.get("redundancy_penalty", 0.0) or 0.0))


def classify(score: float, categories: int, temporal: float) -> str:
    if score >= .80 and categories >= 2 and temporal > 0: return "confirmed"
    if score >= .65 and categories >= 2: return "probable"
    return "insufficient_evidence"
