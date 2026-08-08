from node_health import NodeHealthAggregator


def test_aggregate_healthy():
    """正常指标 → 全部 healthy。"""
    a = NodeHealthAggregator()
    status = a.aggregate("node-1", {
        "cpu_util": 30, "mem_avail_pct": 60, "disk_ok": True, "net_ok": True,
        "cpu_temp": 45, "power_ok": True,
    })
    assert status["cpu"] == "healthy"
    assert status["memory"] == "healthy"
    assert status["disk"] == "healthy"
    assert status["network"] == "healthy"


def test_aggregate_degraded():
    """CPU 高温 → cpu degraded。"""
    a = NodeHealthAggregator()
    status = a.aggregate("node-1", {
        "cpu_util": 30, "mem_avail_pct": 60, "disk_ok": True, "net_ok": True,
        "cpu_temp": 92, "power_ok": True,   # 高温 > 85 → degraded
    })
    assert status["cpu"] == "degraded"


def test_aggregate_fault():
    """磁盘故障 → disk fault。"""
    a = NodeHealthAggregator()
    status = a.aggregate("node-1", {
        "cpu_util": 30, "mem_avail_pct": 60, "disk_ok": False, "net_ok": True,
        "cpu_temp": 40, "power_ok": True,
    })
    assert status["disk"] == "fault"
