from ipmi_ingest import IPMIStore


def test_ipmi_ingest_store():
    """IPMI 上报 → 落库/内存 → 查询。"""
    s = IPMIStore()
    s.ingest("node-1", [
        {"name": "CPU Temp", "type": "Temperature", "reading": "42 C", "status": "ok"},
        {"name": "FAN1", "type": "Fan", "reading": "6000 RPM", "status": "ok"},
    ])
    items = s.query(node="node-1")
    names = [i["sensor_name"] for i in items]
    assert "CPU Temp" in names
    assert "FAN1" in names


def test_ipmi_query_by_type():
    s = IPMIStore()
    s.ingest("node-2", [{"name": "Vcore", "type": "Voltage", "reading": "1.1 V", "status": "ok"}])
    items = s.query(node="node-2", sensor_type="Voltage")
    assert len(items) >= 1 and items[0]["sensor_type"] == "Voltage"
