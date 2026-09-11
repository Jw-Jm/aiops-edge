"""Investigation summary contract (Task 11).

The summary is the *only* sanctioned way a terminal Run reports why it stopped.
It is produced once, at the terminal commit boundary, from the RCA result the
brain already produced. Nothing here may re-derive a root cause, invent
evidence, or read frontend text.

Stable termination reasons (partial is a status, never a reason):
    root_confirmed | evidence_exhausted | budget_exhausted
    source_unavailable | cancelled | runtime_failed
"""

from __future__ import annotations

from typing import Any

SCHEMA_VERSION = 1

TERMINATION_REASONS = (
    "root_confirmed",
    "evidence_exhausted",
    "budget_exhausted",
    "source_unavailable",
    "cancelled",
    "runtime_failed",
)

MAX_CAUSAL_CHAIN = 12

# Error codes that mean a required data source was unavailable. Everything else
# is a runtime failure rather than an evidence problem.
_SOURCE_UNAVAILABLE_CODES = {
    "RCA_V2_UNAVAILABLE",
    "GRAPH_UNAVAILABLE",
    "EVIDENCE_SOURCE_UNAVAILABLE",
    "SOURCE_UNAVAILABLE",
}
_RUNTIME_FAILED_CODES = {
    "BRAIN_EXCEPTION",
    "BRAIN_ERROR",
    "BRAIN_ERROR_EVENT",
    "INVALID_BRAIN_OUTCOME",
    "INVALID_OUTCOME_STATUS",
    "INTERNAL_ERROR",
    "GRAPH_CONTEXT_FINALIZE_FAILED",
}

_BUDGET_KEYS = ("max_steps", "max_tools", "consumed_steps", "consumed_tools")


def _as_int(value: Any, default: int = 0) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def _normalise_budget(raw: Any) -> dict[str, int]:
    budget = raw if isinstance(raw, dict) else {}
    return {key: _as_int(budget.get(key), 0) for key in _BUDGET_KEYS}


def _chain_key(step: dict) -> tuple[str, str, str]:
    return (str(step.get("source_uid") or ""), str(step.get("relation_type") or step.get("relation_label") or ""),
            str(step.get("target_uid") or ""))


def validate_causal_chain(raw: Any, evidence_ids: set[str]) -> tuple[list[dict], list[str]]:
    """Return (chain, warning_codes).

    Every hop must reference a real graph edge (source + target) and every
    evidence id it claims must belong to this Run. An inferred edge keeps
    fact_status=inferred and can never be upgraded into a confirmation.
    """
    warnings: list[str] = []
    if not isinstance(raw, list):
        return [], []
    chain: list[dict] = []
    seen: set[tuple[str, str, str]] = set()
    for item in raw:
        if not isinstance(item, dict):
            continue
        source = str(item.get("source_uid") or "")
        target = str(item.get("target_uid") or "")
        if not source or not target:
            warnings.append("CAUSAL_CHAIN_INCOMPLETE_EDGE")
            continue
        key = _chain_key(item)
        if key in seen:
            continue
        seen.add(key)
        claimed = item.get("evidence_ids")
        ids: list[str] = []
        if isinstance(claimed, list):
            for value in claimed:
                evidence_id = str(value)
                if evidence_id in evidence_ids:
                    ids.append(evidence_id)
                else:
                    warnings.append("CAUSAL_CHAIN_UNKNOWN_EVIDENCE")
        relation = str(item.get("relation_label") or item.get("relation_type") or "关系")
        fact_status = "inferred" if str(item.get("fact_status") or "") == "inferred" else "fact"
        chain.append({
            "source_uid": source,
            "source_name": str(item.get("source_name") or source),
            "relation_type": str(item.get("relation_type") or relation),
            "relation_label": relation,
            "target_uid": target,
            "target_name": str(item.get("target_name") or target),
            "fact_status": fact_status,
            "evidence_ids": ids,
        })
        if len(chain) >= MAX_CAUSAL_CHAIN:
            break
    return chain, warnings


def derive_termination_reason(
    *, status: str, error_code: str = "", budget: dict[str, int] | None = None,
    root_cause_uid: str = "", evidence_ids: list[str] | None = None,
) -> str:
    """Derive the stable termination reason from the outcome itself."""
    budget = budget or {}
    evidence_ids = evidence_ids or []
    if status == "cancelled":
        return "cancelled"
    if status in {"failed", "regressed"}:
        if error_code in _SOURCE_UNAVAILABLE_CODES:
            return "source_unavailable"
        return "runtime_failed"
    if error_code == "BUDGET_EXCEEDED" or (
        budget.get("max_tools", 0) > 0 and budget.get("consumed_tools", 0) >= budget["max_tools"]
    ):
        return "budget_exhausted"
    if error_code in _SOURCE_UNAVAILABLE_CODES:
        return "source_unavailable"
    if root_cause_uid and evidence_ids:
        return "root_confirmed"
    return "evidence_exhausted"


def build_investigation_summary(
    *,
    status: str,
    rca: Any = None,
    budget: Any = None,
    error_code: str = "",
    warning_codes: list[str] | None = None,
) -> dict:
    """Build the durable investigation summary for a terminal Run.

    `rca` is the brain/RCA payload: it may carry `root_cause`,
    `causal_chain`, `evidence_ids`/`evidence` and `missing_evidence`.
    """
    rca_dict = rca if isinstance(rca, dict) else {}
    warnings: list[str] = list(warning_codes or [])

    root_cause_uid = str(rca_dict.get("root_cause") or rca_dict.get("root_cause_uid") or "")

    evidence_ids: list[str] = []
    raw_ids = rca_dict.get("evidence_ids")
    if isinstance(raw_ids, list):
        evidence_ids = [str(value) for value in raw_ids if str(value)]
    if not evidence_ids and isinstance(rca_dict.get("evidence"), list):
        for item in rca_dict["evidence"]:
            if isinstance(item, dict) and item.get("evidence_id"):
                evidence_ids.append(str(item["evidence_id"]))

    chain, chain_warnings = validate_causal_chain(rca_dict.get("causal_chain"), set(evidence_ids))
    warnings.extend(chain_warnings)

    missing = rca_dict.get("missing_evidence")
    missing_evidence = [str(value) for value in missing] if isinstance(missing, list) else []

    budget_summary = _normalise_budget(budget)
    termination = derive_termination_reason(
        status=status, error_code=str(error_code or ""), budget=budget_summary,
        root_cause_uid=root_cause_uid, evidence_ids=evidence_ids,
    )
    if termination not in TERMINATION_REASONS:  # defensive: never leak a raw code
        termination = "runtime_failed"

    # An inferred-only chain or a missing root cause can never be a confirmation.
    if termination == "root_confirmed" and not any(step["fact_status"] == "fact" for step in chain) and chain:
        warnings.append("CAUSAL_CHAIN_INFERRED_ONLY")
        termination = "evidence_exhausted"

    return {
        "schema_version": SCHEMA_VERSION,
        "termination_reason": termination,
        "budget_summary": budget_summary,
        "root_cause_uid": root_cause_uid or None,
        "causal_chain": chain,
        "evidence_ids": evidence_ids,
        "missing_evidence": missing_evidence,
        "warning_codes": list(dict.fromkeys(warnings)),
    }
