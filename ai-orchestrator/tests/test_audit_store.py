from db_audit import AuditStore

a = AuditStore()


def test_log():
    a.log("execute", "admin", "svc-a", "kubectl get pods", "ok", {"n": 1}, "t1")
    assert True


def test_query():
    result = a.query()
    assert isinstance(result, dict)
    assert "items" in result and "total" in result
    if result["total"] > 0:
        assert result["items"][0]["action"] == "execute"


def test_query_filters():
    result = a.query(action="execute", operator="admin")
    assert isinstance(result["items"], list)
