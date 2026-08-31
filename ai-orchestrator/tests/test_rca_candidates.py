from rca_engine.candidates import graph_candidates


def test_graph_candidates_use_bounded_production_limits(monkeypatch):
    calls = []

    def graph_client(**params):
        calls.append(params)
        return {"vertices": [], "edges": []}

    graph_candidates({"entity_uid": "node-1"}, graph_client, max_depth=6)

    assert calls == [{
        "graph_operation": "candidate_subgraph",
        "entity_uid": "node-1",
        "relation_policy": "root_cause_candidate_v1",
        "max_depth": 1,
        "max_vertices": 50,
        "max_edges": 150,
    }]


def test_graph_candidates_operator_limits_are_lower_bounded(monkeypatch):
    monkeypatch.setenv("RCA_GRAPH_MAX_DEPTH", "99")
    monkeypatch.setenv("RCA_GRAPH_MAX_VERTICES", "0")
    monkeypatch.setenv("RCA_GRAPH_MAX_EDGES", "not-a-number")
    calls = []

    graph_candidates({"entity_uid": "node-1"}, lambda **params: calls.append(params) or {})

    assert calls[0]["max_depth"] == 6
    assert calls[0]["max_vertices"] == 1
    assert calls[0]["max_edges"] == 150
