# tests/test_expr.py
from flow_engine.expr import RunContext, resolve_template, resolve_value, eval_condition

def _ctx():
    return RunContext(
        trigger={"service": "deepflow-server"},
        nodes={"n1": {"output": {"result": {"items": [{"name": "a", "err": 3}, {"name": "b", "err": 0}]}, "count": 2}},
               "n2": {"output": {"pass": True}}},
        vars={"threshold": 1},
    )

def test_resolve_simple_trigger():
    assert resolve_template("svc={{trigger.service}}", _ctx()) == "svc=deepflow-server"

def test_resolve_path_with_index():
    assert resolve_template("first={{nodes.n1.output.result.items[0].name}}", _ctx()) == "first=a"

def test_resolve_vars():
    assert resolve_template("t={{vars.threshold}}", _ctx()) == "t=1"

def test_resolve_unknown_keeps_placeholder():
    assert resolve_template("{{nodes.nope.output.x}}", _ctx()) == "{{nodes.nope.output.x}}"

def test_resolve_value_passthrough_nonstring():
    assert resolve_value(42, _ctx()) == 42

def test_eval_gt():
    assert eval_condition("{{nodes.n1.output.result.items[0].err}} > {{vars.threshold}}", _ctx()) is True

def test_eval_contains():
    assert eval_condition("{{trigger.service}} contains deepflow", _ctx()) is True
    assert eval_condition("{{trigger.service}} contains nope", _ctx()) is False

def test_eval_eq_string():
    assert eval_condition("{{trigger.service}} == deepflow-server", _ctx()) is True
