from db_agents import AgentStore, ReportStore, KnowledgeStore, RuleStore

a, r, k, rl = AgentStore(), ReportStore(), KnowledgeStore(), RuleStore()


def test_agents():
    a.upsert("ops-helper", "运维助手", "诊断", "资深SRE", True, False)
    agents = a.list()
    assert any(x["name"] == "ops-helper" for x in agents)


def test_agent_toggle():
    a.toggle("ops-helper")
    agents = a.list()
    assert any(x["name"] == "ops-helper" and not x["enabled"] for x in agents)


def test_knowledge():
    k.add("如何排查 CPU 高", "用 top 查看", "manual", "cpu,排查")
    res = k.search("cpu")
    assert len(res["items"]) >= 1


def test_rules():
    rl.save("cpu_high", "CPU 高", "metric", "warning", True, "global", "all",
            {"expr": "cpu>80"}, "custom")
    rules = rl.list()
    assert any(x["rule_key"] == "cpu_high" for x in rules)


def test_rule_toggle():
    rl.toggle("cpu_high")
    rules = rl.list()
    assert any(x["rule_key"] == "cpu_high" and not x["enabled"] for x in rules)


def test_reports():
    r.save({"task_id": "rt1", "service_name": "svc", "report_type": "inspection",
            "verdict": "ok", "risk_score": 0.0, "summary": "s", "content": "c"})
    assert len(r.list()["items"]) >= 1
