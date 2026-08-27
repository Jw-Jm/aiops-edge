from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Mapping

from .candidates import candidate_rows, graph_candidates
from .context import GraphContext
from .entity_resolver import resolve_entity
from .evidence import evidence_for_candidate, independent_categories
from .explanation import explain
from .scorer import classify, score_candidate


@dataclass(frozen=True)
class RCARequest:
    run_id: str
    tenant_id: str
    cluster_id: str
    resource_id: str = ""
    entity_uid: str = ""
    entity_name: str = ""
    symptoms: tuple[str, ...] = ()
    evidence: tuple[Mapping[str, Any], ...] = ()


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

    def to_dict(self) -> dict[str, Any]:
        return {"run_id": self.run_id, "root_cause": self.root_cause, "root_cause_status": self.root_cause_status,
                "confidence": self.confidence, "root_cause_scope": self.root_cause_scope,
                "graph_enhanced": self.graph_enhanced, "candidate_roots": self.candidate_roots,
                "propagation_paths": self.propagation_paths, "evidence": self.evidence,
                "missing_evidence": self.missing_evidence, "contradictions": self.contradictions,
                "graph_context": self.graph_context, "explanation": self.explanation}


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
        context = GraphContext(request.run_id, request.tenant_id, request.cluster_id)
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
            result = RCAResult(request.run_id, None, "insufficient_evidence", 0.0, "local_only", False,
                               evidence=evidence, missing_evidence=["graph_relation"],
                               explanation=explain({"root_cause_status": "insufficient_evidence", "evidence": evidence}))
            result.graph_context = {"partial": True, "stale": True, "warning_codes": ["GRAPH_UNAVAILABLE", str(exc)[:100]],
                                    "events": ["graph_context_created"]}
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
            breakdown = score_candidate(candidate, candidate_evidence, hops=candidate.get("hops", 1))
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
        payload = {"root_cause_status": status, "root_cause": top["entity_uid"] if top else None,
                   "candidate_roots": ranked[:5], "propagation_paths": context.edges, "evidence": evidence,
                   "missing_evidence": [] if top and top["evidence_categories"] >= 2 else ["independent_evidence"],
                   "contradictions": []}
        if status == "confirmed":
            context.record("graph_context_finalized")
        result = RCAResult(request.run_id, top["entity_uid"] if top else None, status,
                           round(top["score"], 4) if top else 0.0, candidate_roots=ranked[:5],
                           propagation_paths=context.edges, evidence=evidence,
                           missing_evidence=payload["missing_evidence"], graph_context=context.to_dict(),
                           explanation=explain(payload, self.llm))
        if self.persistence is not None:
            self.persistence(result.to_dict(), context.to_dict())
        return result


def diagnose_root_cause_v2(req: RCARequest, execution_context: Any, *, graph_client: Any,
                           evidence_provider: Any = None, persistence: Any = None, llm: Any = None) -> RCAResult:
    return RCAEngineV2(graph_client=graph_client, evidence_provider=evidence_provider,
                       persistence=persistence, llm=llm).diagnose(req, execution_context)
