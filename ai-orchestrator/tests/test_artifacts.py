import pytest
from artifacts import list_artifacts, TYPE_LABELS, REPORT, APPROVAL, FLOW_RUN


def test_type_labels():
    assert TYPE_LABELS[REPORT] == "报告"
    assert TYPE_LABELS[APPROVAL] == "审批"
    assert TYPE_LABELS[FLOW_RUN] == "工作流"


def test_list_artifacts_returns_list():
    # 无 MySQL/SQLite 时静默降级为空列表，不应抛异常
    items = list_artifacts(limit=10)
    assert isinstance(items, list)


def test_artifact_uniform_structure():
    # 验证聚合项统一结构（无论是否空，结构字段定义在模块中）
    from artifacts import list_reports, list_approvals, list_flow_runs
    for fn in (list_reports, list_approvals, list_flow_runs):
        try:
            items = fn(limit=5)
        except Exception as e:
            pytest.fail(f"{fn.__name__} raised: {e}")
        assert isinstance(items, list)
        for it in items:
            for field in ("type", "id", "title", "status", "service", "time", "summary", "detail_url"):
                assert field in it, f"artifact missing {field}"
