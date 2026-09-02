from __future__ import annotations

from dataclasses import dataclass, field
import hashlib
from pathlib import Path
from typing import Any, Mapping

from .candidates import candidate_rows, graph_candidates, propagation_paths
from .context import GraphContext
from .entity_resolver import resolve_entity
from .evidence import evidence_for_candidate, independent_categories, unbound_context
from .contradictions import evaluate_contradictions
from .explanation import explain
from .scorer import classify, normalize_timestamp, parse_timestamp, score_candidate, timestamp_text


def _policy_digest() -> str:
    policy_path = Path(__file__).with_name("policies") / "v1.json"
    try:
        payload = policy_path.read_bytes()
    except OSError:
        payload = b'{"version":"v1"}'
    return hashlib.sha256(payload).hexdigest()


@dataclass(frozen=True, init=False)
class RCARequest:
    """Canonical immutable RCA input (V9.2 §20.3).

    The public constructor uses ``cluster_ids``, ``target_type`` and
    ``target_resource_id`` exactly as the persisted Run contract specifies.
    ``cluster_id``/``resource_id`` remain accepted as keyword-only aliases for
    older development callers; they are projected into the canonical fields
    and never become a second source of truth.
    """
    run_id: str
    tenant_id: str
    cluster_ids: tuple[str, ...]
    target_type: str
    target_resource_id: str
    window_start: str
    window_end: str
    symptom_time: str
    entity_uid: str = ""
    entity_name: str = ""
    symptoms: tuple[str, ...] = ()
    evidence: tuple[Mapping[str, Any], ...] = ()

    def __init__(
        self,
        run_id: str,
        tenant_id: str,
        cluster_ids: tuple[str, ...] | list[str] | str | None = None,
        target_type: str = "service",
        target_resource_id: str = "",
        window_start: Any = None,
        window_end: Any = None,
        symptom_time: Any = None,
        *,
        entity_uid: str = "",
        entity_name: str = "",
        symptoms: tuple[str, ...] = (),
        evidence: tuple[Mapping[str, Any], ...] = (),
        cluster_id: str | None = None,
        resource_id: str | None = None,
        target_type_hint: str | None = None,
    ) -> None:
        if not str(run_id or ""):
            raise ValueError("run_id is required")
        if not str(tenant_id or ""):
            raise ValueError("tenant_id is required")
        if cluster_ids is None:
            canonical_clusters = (str(cluster_id),) if cluster_id else ()
        elif isinstance(cluster_ids, str):
            canonical_clusters = (cluster_ids,)
        else:
            canonical_clusters = tuple(str(item) for item in cluster_ids if str(item))
        if cluster_id and canonical_clusters != (str(cluster_id),):
            raise ValueError("cluster_id and cluster_ids disagree")
        canonical_target = str(target_resource_id or resource_id or "")
        if target_resource_id and resource_id and str(target_resource_id) != str(resource_id):
            raise ValueError("resource_id and target_resource_id disagree")
        if target_type_hint and target_type == "service":
            target_type = target_type_hint
        if window_start is None or window_end is None:
            raise ValueError("window_start and window_end are required")
        if symptom_time is None:
            symptom_time = window_end
        # The run's persisted ai_runs range is the sole source of truth for a
        # replay. Normalize once at the boundary so providers cannot re-anchor
        # an investigation to wall-clock ``now``.
        start = parse_timestamp(window_start)
        end = parse_timestamp(window_end)
        if start is None:
            raise ValueError("window_start must be an ISO-8601 timestamp")
        if end is None:
            raise ValueError("window_end must be an ISO-8601 timestamp")
        if start > end:
            raise ValueError("window_start must not be later than window_end")
        symptom = parse_timestamp(symptom_time)
        if symptom is None:
            raise ValueError("symptom_time must be an ISO-8601 timestamp")
        if symptom < start or symptom > end:
            raise ValueError("symptom_time must fall within the frozen ai_run window")
        object.__setattr__(self, "run_id", str(run_id))
        object.__setattr__(self, "tenant_id", str(tenant_id))
        object.__setattr__(self, "cluster_ids", canonical_clusters)
        object.__setattr__(self, "target_type", str(target_type or "service"))
        object.__setattr__(self, "target_resource_id", canonical_target)
        object.__setattr__(self, "window_start", normalize_timestamp(start, field="window_start"))
        object.__setattr__(self, "window_end", normalize_timestamp(end, field="window_end"))
        object.__setattr__(self, "symptom_time", normalize_timestamp(symptom, field="symptom_time"))
        object.__setattr__(self, "entity_uid", str(entity_uid or ""))
        object.__setattr__(self, "entity_name", str(entity_name or ""))
        object.__setattr__(self, "symptoms", tuple(symptoms or ()))
        object.__setattr__(self, "evidence", tuple(evidence or ()))

    @property
    def cluster_id(self) -> str:
        """Legacy single-cluster view of the canonical cluster tuple."""
        return self.cluster_ids[0] if self.cluster_ids else ""

    @property
    def resource_id(self) -> str:
        """Legacy resource alias; canonical storage uses target_resource_id."""
        return self.target_resource_id

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
        persisted_clusters = read("cluster_ids")
        if persisted_clusters is None:
            persisted_cluster = read("primary_cluster_id", read("cluster_id", ""))
            persisted_clusters = (persisted_cluster,) if persisted_cluster else ()
        values: dict[str, Any] = {
            "run_id": read("run_id"), "tenant_id": read("tenant_id"),
            "cluster_ids": persisted_clusters,
            "target_type": read("target_type", "service"),
            "target_resource_id": read("target_resource_id", ""),
            "window_start": start, "window_end": end, "symptom_time": symptom,
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
    policy_version: str = "v1"
    policy_digest: str = ""
    context_evidence: list[dict[str, Any]] = field(default_factory=list)

    def to_dict(self) -> dict[str, Any]:
        payload = {"run_id": self.run_id, "root_cause": self.root_cause, "root_cause_status": self.root_cause_status,
                   "confidence": self.confidence, "root_cause_scope": self.root_cause_scope,
                   "graph_enhanced": self.graph_enhanced, "candidate_roots": self.candidate_roots,
                   "propagation_paths": self.propagation_paths, "evidence": self.evidence,
                   "missing_evidence": self.missing_evidence, "contradictions": self.contradictions,
                   "graph_context": self.graph_context, "explanation": self.explanation,
                   "window_start": self.window_start, "window_end": self.window_end,
                   "symptom_time": self.symptom_time, "policy_version": self.policy_version,
                   "policy_digest": self.policy_digest, "context_evidence": self.context_evidence}
        # The evidence validator consumes the RCA result itself.  Expose the
        # deterministic fields explicitly instead of requiring a second,
        # lossy adapter to reconstruct them from ``confidence`` and graph
        # internals.  A final context is emitted only for a genuinely
        # confirmed, complete graph result; partial/probable results remain
        # fail-closed and intentionally omit finality claims.
        context = self.graph_context if isinstance(self.graph_context, dict) else {}
        vertices = context.get("vertices") if isinstance(context.get("vertices"), list) else []
        if self.root_cause_status == "confirmed" and not context.get("partial") and not context.get("stale"):
            final_context = dict(context)
            final_context["final"] = True
            payload["final_graph_context"] = final_context
            payload["propagation_path"] = dict(self.propagation_paths[0]) if self.propagation_paths else {}
            payload["subgraph_node_count"] = len(vertices)
            score = float(self.confidence)
            payload["root_score"] = score
            payload["deterministic_root_score"] = score
        return payload


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
            context.symptom_entity_uid = str(entity.get("entity_uid") or "")
            context.record("graph_context_created")
            subgraph = graph_candidates(entity, self._graph_call)
            context.vertices = list(subgraph.get("vertices") or [])
            context.edges = list(subgraph.get("edges") or [])
            # Query-owned graph reads carry the projection generation in the
            # typed graph meta envelope. Persist it with the RCA context so a
            # replay can identify the exact graph generation consumed. Older
            # adapters may not emit meta; keep the value at zero rather than
            # inventing provenance.
            graph_meta = subgraph.get("meta") if isinstance(subgraph, Mapping) else None
            if isinstance(graph_meta, Mapping):
                try:
                    context.graph_generation = max(0, int(graph_meta.get("graph_generation") or 0))
                except (TypeError, ValueError):
                    context.graph_generation = 0
            context.record("rca_candidates_ranked")
        except Exception as exc:  # Graph down must become explicit local-only RCA.
            evidence = [dict(item) for item in request.evidence]
            result = RCAResult(run_id=request.run_id, root_cause=None, root_cause_status="insufficient_evidence",
                               confidence=0.0, root_cause_scope="local_only", graph_enhanced=False,
                               evidence=evidence, missing_evidence=["graph_relation"],
                               graph_context=context.to_dict(), window_start=window_start,
                               window_end=window_end, symptom_time=symptom_time,
                               policy_version="v1", policy_digest=_policy_digest(),
                               context_evidence=unbound_context(evidence),
                               explanation=explain({"root_cause_status": "insufficient_evidence", "evidence": evidence}))
            context.partial = True
            context.stale = True
            context.warning_codes = ["GRAPH_UNAVAILABLE"]
            context.record("graph_context_created")
            result.graph_context = context.to_dict()
            if self.persistence is not None:
                try:
                    # Persist the local-only context as well.  A graph outage
                    # must remain replayable/auditable instead of silently
                    # disappearing before the worker records its terminal
                    # state.
                    self.persistence(result.to_dict(), result.graph_context)
                except Exception as persist_exc:  # noqa: BLE001 - preserve partial RCA
                    context.partial = True
                    context.warning_codes.append("GRAPH_CONTEXT_PERSIST_FAILED")
                    result.missing_evidence.append("graph_context_persistence")
                    result.graph_context = context.to_dict()
            return result

        evidence = [dict(item) for item in request.evidence]
        if self.evidence_provider is not None:
            fetched = self.evidence_provider(request, context)
            if fetched:
                evidence.extend(fetched)
        provider_failures = list(getattr(self.evidence_provider, "failures", []) or [])
        if provider_failures:
            context.partial = True
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
        contradictions = evaluate_contradictions(top, top.get("evidence", []), symptom_time=request.symptom_time,
                                                  window_end=request.window_end) if top else []
        status = classify(top["score"], top["evidence_categories"], top["score_breakdown"]["temporal"]) if top else "insufficient_evidence"
        if contradictions:
            status = "insufficient_evidence"
        if top and second and top["score"] - second["score"] < .05 and status == "confirmed":
            status = "multiple_probable_roots"
        context.record("root_cause_selected") if top else None
        paths = propagation_paths(subgraph, top["entity_uid"], str(entity.get("entity_uid") or "")) if top else []
        context.propagation_paths = paths
        context.record("propagation_paths_built") if paths else None
        missing_evidence = [] if top and top["evidence_categories"] >= 2 else ["independent_evidence"]
        if provider_failures:
            missing_evidence.append("evidence_source_unavailable")
        payload = {"root_cause_status": status, "root_cause": top["entity_uid"] if top else None,
                   "candidate_roots": ranked[:5], "propagation_paths": paths, "evidence": evidence,
                   "missing_evidence": missing_evidence,
                   "contradictions": contradictions}
        if status == "confirmed":
            context.record("graph_context_finalized")
        result = RCAResult(run_id=request.run_id, root_cause=top["entity_uid"] if top else None,
                           root_cause_status=status, confidence=round(top["score"], 4) if top else 0.0,
                           candidate_roots=ranked[:5], propagation_paths=paths, evidence=evidence,
                           missing_evidence=payload["missing_evidence"], graph_context=context.to_dict(),
                           window_start=window_start, window_end=window_end,
                           symptom_time=symptom_time, contradictions=contradictions,
                           policy_version="v1", policy_digest=_policy_digest(),
                           context_evidence=unbound_context(evidence),
                           explanation=explain(payload, self.llm))
        if self.persistence is not None:
            self.persistence(result.to_dict(), context.to_dict())
        return result


def diagnose_root_cause_v2(req: RCARequest, execution_context: Any, *, graph_client: Any,
                           evidence_provider: Any = None, persistence: Any = None, llm: Any = None) -> RCAResult:
    return RCAEngineV2(graph_client=graph_client, evidence_provider=evidence_provider,
                       persistence=persistence, llm=llm).diagnose(req, execution_context)
