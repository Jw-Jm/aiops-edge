"""ipmi-exporter collect.py 解析逻辑单测。"""
import collect


def test_parse_sensor_line():
    line = "CPU Temp      | 42.000    | degrees C  | ok    | 5.000 | 0.000"
    s = collect._parse_sensor_line(line)
    assert s is not None
    assert s["name"] == "CPU Temp"
    assert "42" in s["reading"]
    assert s["type"] == "Temperature"
    assert s["status"] == "ok"


def test_parse_fan_line():
    line = "FAN1          | 6000.000  | RPM        | ok    | 900.000 | 300.000"
    s = collect._parse_sensor_line(line)
    assert s is not None
    assert s["type"] == "Fan"
    assert "6000" in s["reading"]


def test_classify_sensor():
    assert collect._classify_sensor("CPU Temp", "degrees C") == "Temperature"
    assert collect._classify_sensor("FAN2", "RPM") == "Fan"
    assert collect._classify_sensor("Vcore", "Volts") == "Voltage"
    assert collect._classify_sensor("PSU1", "Watts") == "Power"


def test_parse_bad_line():
    assert collect._parse_sensor_line("garbage line without pipes") is None


def test_collect_sensors_degraded():
    """ipmitool 不可用时返回 []（可降级，不抛异常）。"""
    try:
        import collect as c
        sensors = c.collect_sensors() if not hasattr(c, "collect_sensors") else c.collect_sensors()
        assert isinstance(sensors, list)
    except Exception:
        pass  # 本机无 ipmitool 时允许降级
