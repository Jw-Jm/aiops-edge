from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Mapping

from .candidates import candidate_rows, graph_candidates, propagation_paths
from .context import GraphContext
from .entity_resolver import resolve_entity
from .evidence import evidence_for_candidate, independent_categories
from .explanation import explain
from .scorer import classify, normalize_timestamp, parse_timestamp, score_candidate, timestamp_text


@dataclass(frozen=True)
class RCARequest:
    run_id: str
    tenant_id: str
    cluster_id: str
    window_start: str
    window_end: str
    symptom_time: str
    resource_id: str = ""
    entity_uid: str = ""
    entity_name: str = ""
    symptoms: tuple[str, ...] = ()
    evidence: tuple[Mapping[str, Any], ...] = ()

    def __post_init__(self) -> None:
        # The run's persisted ai_runs range is the sole source of truth for a
        # replay. Normalize once at the boundary so providers cannot re-anchor
        # an investigation to wall-clock ``now``.
        start = parse_timestamp(self.window_start)
        end = parse_timestamp(self.window_end)
        if start is None:
            raise ValueError("window_start must be an ISO-8601 timestamp")
        if end is None:
            raise ValueError("window_end must be an ISO-8601 timestamp")
        if start > end:
            raise ValueError("window_start must not be later than window_end")
        symptom = parse_timestamp(self.symptom_time)
        if symptom is None:
            raise ValueError("symptom_time must be an ISO-8601 timestamp")
        if symptom < start or symptom > end:
            raise ValueError("symptom_time must fall within the frozen ai_run window")
        object.__setattr__(self, "window_start", normalize_timestamp(start, field="window_start"))
        object.__setattr__(self, "window_end", normalize_timestamp(end, field="window_end"))
        object.__setattr__(self, "symptom_time", normalize_timestamp(symptom, field="symptom_time"))

    @classmethod
    def from_ai_run(cls, ai_run: Mapping[str, Any] | Any, **overrides: Any) -> "RCARequest":
        """Build an RCA request from the immutable ``ai_runs`` snapshot.

        Callers may override target identity/evidence, but the persisted run
        time range is always copied and validated at this boundary.  When a
        separate symptom marker is unavailable, the run's frozen end is the
        deterministic symptom timestamp rather than a wall-clock calculation.
        """
        def read(name: str, default: Any = None) -> Any:
            if isinstance(ai_run, Mapping):
                return ai_run.get(name, default)
            return getattr(ai_run, name, default)

        start = read("time_range_start")
        end = read("time_range_end")
        symptom = read("symptom_time", end)
        if start is None or end is None:
            raise ValueError("ai_run must contain time_range_start and time_range_end")
        allowed_overrides = {"resource_id", "entity_uid", "entity_name", "symptoms", "evidence"}
        forbidden_overrides = set(overrides) - allowed_overrides
        if forbidden_overrides:
            raise ValueError("RCA run identity and time window are immutable and must come from ai_run")
        values: dict[str, Any] = {
            "run_id": read("run_id"), "tenant_id": read("tenant_id"),
            "cluster_id": read("primary_cluster_id", read("cluster_id", "")),
            "window_start": start, "window_end": end, "symptom_time": symptom,
            "resource_id": read("target_resource_id", ""),
        }
        values.update(overrides)
        return cls(**values)


@dataclass
class RCAResult:
    run_id: str
    root_cause: str | None
    root_cause_status: str
    confidence: float
    root_cause_scope: str = "graph_enhanced"
    graph_enhanced: bool = True
    candidate_roots: list[dict[str, Any]] = field(default_factory=list)
    propagation_paths: list[dict[str, Any]] = field(default_factory=list)
    evidence: list[dict[str, Any]] = field(default_factory=list)
    missing_evidence: list[str] = field(default_factory=list)
    contradictions: list[dict[str, Any]] = field(default_factory=list)
    graph_context: dict[str, Any] | None = None
    explanation: str = ""
    window_start: str = ""
    window_end: str = ""
    symptom_time: str = ""

    def to_dict(self) -> dict[str, Any]:
        return {"run_id": self.run_id, "root_cause": self.root_cause, "root_cause_status": self.root_cause_status,
                "confidence": self.confidence, "root_cause_scope": self.root_cause_scope,
                "graph_enhanced": self.graph_enhanced, "candidate_roots": self.candidate_roots,
                "propagation_paths": self.propagation_paths, "evidence": self.evidence,
                "missing_evidence": self.missing_evidence, "contradictions": self.contradictions,
                "graph_context": self.graph_context, "explanation": self.explanation,
                "window_start": self.window_start, "window_end": self.window_end,
                "symptom_time": self.symptom_time}


class RCAEngineV2:
    """Entity Resolution → Graph Candidate → Evidence → Score → Classify → Persist → Explain."""

    def __init__(self, *, graph_client: Any, evidence_provider: Any = None, persistence: Any = None, llm: Any = None):
        self.graph_client = graph_client
        self.evidence_provider = evidence_provider
        self.persistence = persistence
        self.llm = llm

    def _graph_call(self, **params: Any) -> dict[str, Any]:
        return self.graph_client(**params)

    def diagnose(self, request: RCARequest, execution_context: Any = None) -> RCAResult:
        window_start = timestamp_text(request.window_start)
        window_end = timestamp_text(request.window_end)
        symptom_time = timestamp_text(request.symptom_time)
        context = GraphContext(request.run_id, request.tenant_id, request.cluster_id,
                               window_start=window_start, window_end=window_end,
                               symptom_time=symptom_time)
        try:
            entity = resolve_entity(request, self._graph_call)
            if not entity:
                raise ValueError("GRAPH_ENTITY_NOT_FOUND")
            context.record("graph_context_created")
            subgraph = graph_candidates(entity, self._graph_call)
            context.vertices = list(subgraph.get("vertices") or [])
            context.edges = list(subgraph.get("edges") or [])
            context.record("rca_candidates_ranked")
        except Exception as exc:  # Graph down must become explicit local-only RCA.
            evidence = [dict(item) for item in request.evidence]
            result = RCAResult(run_id=request.run_id, root_cause=None, root_cause_status="insufficient_evidence",
                               confidence=0.0, root_cause_scope="local_only", graph_enhanced=False,
                               evidence=evidence, missing_evidence=["graph_relation"],
                               graph_context=context.to_dict(), window_start=window_start,
                               window_end=window_end, symptom_time=symptom_time,
                               explanation=explain({"root_cause_status": "insufficient_evidence", "evidence": evidence}))
            context.partial = True
            context.stale = True
            context.warning_codes = ["GRAPH_UNAVAILABLE", str(exc)[:100]]
            context.record("graph_context_created")
            result.graph_context = context.to_dict()
            return result

        evidence = [dict(item) for item in request.evidence]
        if self.evidence_provider is not None:
            fetched = self.evidence_provider(request, context)
            if fetched:
                evidence.extend(fetched)
        ranked = []
        for candidate in candidate_rows(subgraph):
            uid = str(candidate.get("entity_uid") or "")
            candidate_evidence = evidence_for_candidate(evidence, uid)
            breakdown = score_candidate(candidate, candidate_evidence, hops=candidate.get("hops", 1),
                                        symptom_time=request.symptom_time, window_start=request.window_start,
                                        window_end=request.window_end)
            ranked.append({"entity_uid": uid, "name": candidate.get("name", uid), "entity_type": candidate.get("entity_type", ""),
                           "score": breakdown.score, "score_breakdown": breakdown.to_dict(),
                           "evidence_categories": independent_categories(candidate_evidence),
                           "evidence": candidate_evidence})
        ranked.sort(key=lambda row: (-row["score"], row["entity_uid"]))
        top = ranked[0] if ranked else None
        second = ranked[1] if len(ranked) > 1 else None
        status = classify(top["score"], top["evidence_categories"], top["score_breakdown"]["temporal"]) if top else "insufficient_evidence"
        if top and second and top["score"] - second["score"] < .05 and status == "confirmed":
            status = "multiple_probable_roots"
        context.record("root_cause_selected") if top else None
        paths = propagation_paths(subgraph, top["entity_uid"], str(entity.get("entity_uid") or "")) if top else []
        context.propagation_paths = paths
        context.record("propagation_paths_built") if paths else None
        payload = {"root_cause_status": status, "root_cause": top["entity_uid"] if top else None,
                   "candidate_roots": ranked[:5], "propagation_paths": paths, "evidence": evidence,
                   "missing_evidence": [] if top and top["evidence_categories"] >= 2 else ["independent_evidence"],
                   "contradictions": []}
        if status == "confirmed":
            context.record("graph_context_finalized")
        result = RCAResult(run_id=request.run_id, root_cause=top["entity_uid"] if top else None,
                           root_cause_status=status, confidence=round(top["score"], 4) if top else 0.0,
                           candidate_roots=ranked[:5], propagation_paths=paths, evidence=evidence,
                           missing_evidence=payload["missing_evidence"], graph_context=context.to_dict(),
                           window_start=window_start, window_end=window_end,
                           symptom_time=symptom_time, explanation=explain(payload, self.llm))
        if self.persistence is not None:
            self.persistence(result.to_dict(), context.to_dict())
        return result


def diagnose_root_cause_v2(req: RCARequest, execution_context: Any, *, graph_client: Any,
                           evidence_provider: Any = None, persistence: Any = None, llm: Any = None) -> RCAResult:
    return RCAEngineV2(graph_client=graph_client, evidence_provider=evidence_provider,
                       persistence=persistence, llm=llm).diagnose(req, execution_context)
