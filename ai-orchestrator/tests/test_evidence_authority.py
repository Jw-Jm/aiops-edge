import pytest
from fastapi import HTTPException


def test_orchestrator_evidence_registry_routes_are_disabled():
    from ai_runs_api import list_run_evidences, get_run_evidence

    with pytest.raises(HTTPException) as exc:
        list_run_evidences("22222222-2222-4222-8222-222222222222")
    assert exc.value.status_code == 410
    with pytest.raises(HTTPException) as exc:
        get_run_evidence("22222222-2222-4222-8222-222222222222", "ev-1")
    assert exc.value.status_code == 410
