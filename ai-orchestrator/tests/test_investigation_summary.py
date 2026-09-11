"""Tests for the durable investigation summary contract (Task 11)."""

from investigation_summary import (
    MAX_CAUSAL_CHAIN,
    build_investigation_summary,
    derive_termination_reason,
)


def test_budget_exhaustion_commits_truthful_partial_summary():
    result = build_investigation_summary(
        status="partial",
        error_code="BUDGET_EXCEEDED",
        budget={"max_steps": 10, "max_tools": 16, "consumed_steps": 10, "consumed_tools": 16},
        rca={"evidence": [{"evidence_id": "ev-1"}]},
    )
    assert result["termination_reason"] == "budget_exhausted"
    assert result["budget_summary"]["consumed_tools"] == 16
    assert result["budget_summary"]["max_tools"] == 16
    assert result["evidence_ids"] == ["ev-1"]
    assert result["schema_version"] == 1


def test_confirmed_root_cause_maps_to_root_confirmed():
    result = build_investigation_summary(
        status="success",
        rca={"root_cause": "uid-pod-1", "evidence_ids": ["e-1"],
             "causal_chain": [{"source_uid": "u0", "target_uid": "uid-pod-1",
                               "relation_type": "USES", "relation_label": "使用",
                               "fact_status": "fact", "evidence_ids": ["e-1"]}]},
        budget={"max_steps": 10, "max_tools": 16, "consumed_steps": 4, "consumed_tools": 6},
    )
    assert result["termination_reason"] == "root_confirmed"
    assert result["root_cause_uid"] == "uid-pod-1"
    assert result["causal_chain"][0]["fact_status"] == "fact"
    assert result["causal_chain"][0]["evidence_ids"] == ["e-1"]


def test_exhausted_evidence_without_root_cause_is_not_confirmed():
    result = build_investigation_summary(status="partial", rca={"evidence_ids": ["e-1"]})
    assert result["termination_reason"] == "evidence_exhausted"
    assert result["root_cause_uid"] is None


def test_unavailable_source_and_runtime_failure_are_distinct():
    assert build_investigation_summary(status="failed", error_code="RCA_V2_UNAVAILABLE")["termination_reason"] == "source_unavailable"
    assert build_investigation_summary(status="failed", error_code="BRAIN_EXCEPTION")["termination_reason"] == "runtime_failed"
    assert build_investigation_summary(status="cancelled")["termination_reason"] == "cancelled"


def test_unknown_evidence_reference_is_dropped_with_warning():
    result = build_investigation_summary(
        status="success",
        rca={"root_cause": "uid-x", "evidence_ids": ["e-1"],
             "causal_chain": [{"source_uid": "a", "target_uid": "b", "evidence_ids": ["ghost"]}]},
    )
    assert result["causal_chain"][0]["evidence_ids"] == []
    assert "CAUSAL_CHAIN_UNKNOWN_EVIDENCE" in result["warning_codes"]


def test_inferred_only_chain_cannot_confirm_root_cause():
    result = build_investigation_summary(
        status="success",
        rca={"root_cause": "uid-x", "evidence_ids": ["e-1"],
             "causal_chain": [{"source_uid": "a", "target_uid": "b", "fact_status": "inferred",
                               "evidence_ids": ["e-1"]}]},
    )
    assert result["termination_reason"] != "root_confirmed"
    assert "CAUSAL_CHAIN_INFERRED_ONLY" in result["warning_codes"]


def test_duplicate_hops_are_deduplicated_and_capped():
    hop = {"source_uid": "a", "target_uid": "b", "relation_type": "USES"}
    chain = [dict(hop) for _ in range(30)]
    result = build_investigation_summary(status="partial", rca={"causal_chain": chain})
    assert len(result["causal_chain"]) == 1

    many = [dict(hop, target_uid=f"t{i}") for i in range(MAX_CAUSAL_CHAIN + 5)]
    result = build_investigation_summary(status="partial", rca={"causal_chain": many})
    assert len(result["causal_chain"]) == MAX_CAUSAL_CHAIN


def test_derive_termination_reason_prefers_budget_gate():
    budget = {"max_tools": 16, "consumed_tools": 16}
    assert derive_termination_reason(status="partial", budget=budget) == "budget_exhausted"
    assert derive_termination_reason(status="partial", budget={"max_tools": 16, "consumed_tools": 2}) == "evidence_exhausted"
