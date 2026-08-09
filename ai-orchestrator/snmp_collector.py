"""SNMP 采集器 — 采集管理网上联交换机（走管理网，四网段隔离下仅管理面）。

安全与降级：
- 只读 OID，不写设备
- 无设备 / 网络不通 / pysnmp 不可用 → 静默降级，不阻塞
- community 从配置读取，不落库（表存的是设备记录，密码类仅存 community 名可空）
"""
import asyncio
import os
import time

_COLLECT_INTERVAL = int(os.environ.get("SNMP_COLLECT_INTERVAL", "60"))
_TIMEOUT = float(os.environ.get("SNMP_TIMEOUT", "3"))
_SNMP_COMMUNITY = os.environ.get("SNMP_COMMUNITY", "public")


def _run_async_coroutine(coro):
    """安全地执行一个 asyncio 协程。

    若当前已在运行中的事件循环（例如被 async run_forever 调用），直接 asyncio.run
    会抛 RuntimeError。这里统一放到线程中执行 asyncio.run，避免阻塞/崩溃。
    """
    try:
        asyncio.get_running_loop()
        import concurrent.futures
        executor = concurrent.futures.ThreadPoolExecutor(max_workers=1)
        try:
            return executor.submit(asyncio.run, coro).result()
        finally:
            executor.shutdown(wait=False)
    except RuntimeError:
        # 无运行中的事件循环
        return asyncio.run(coro)


class OIDS:
    """SNMP 只读 OID 表（IF-MIB / UCD-SNMP-MIB）。"""
    SYS_DESCR = ".1.3.6.1.2.1.1.1.0"
    IF_TABLE = ".1.3.6.1.2.1.2.2.1"
    IF_NAME = ".1.3.6.1.2.1.2.2.1.2"
    IF_OPER = ".1.3.6.1.2.1.2.2.1.8"
    IF_ADMIN = ".1.3.6.1.2.1.2.2.1.7"
    IF_IN_OCT = ".1.3.6.1.2.1.2.2.1.10"
    IF_OUT_OCT = ".1.3.6.1.2.1.2.2.1.16"
    IF_ERR_IN = ".1.3.6.1.2.1.2.2.1.14"
    IF_SPEED = ".1.3.6.1.2.1.2.2.1.5"


class SNMPCollector:
    """SNMP 采集器。collect_all() 遍历 active 设备，可降级。"""

    def __init__(self):
        self._running = False

    # ── pysnmp 封装（可注入替换，测试用）──────────────
    def get_oid(self, oid: str, dev: dict) -> dict:
        """轮询单个 OID 子树，返回 {oid: value}。网络/库不可用返回 {}。"""
        try:
            from pysnmp.hlapi.v3arch.asyncio import SnmpEngine, CommunityData, UdpTransportTarget, ContextData
            from pysnmp.hlapi.v3arch.asyncio import getCmd, nextCmd, ObjectType, ObjectIdentity

            community = dev.get("community") or _SNMP_COMMUNITY
            host = dev.get("ip")

            async def _walk():
                results = {}
                engine = SnmpEngine()
                errorIndication, errorStatus, errorIndex, varBinds = await getCmd(
                    engine,
                    CommunityData(community),
                    UdpTransportTarget((host, 161), timeout=_TIMEOUT),
                    ContextData(),
                    ObjectType(ObjectIdentity(oid)),
                )
                if errorIndication or errorStatus:
                    return results
                for name, val in varBinds:
                    results[str(name)] = str(val)
                return results

            return _run_async_coroutine(_walk())
        except Exception:
            return {}

    def _collect_interfaces(self, dev: dict) -> list:
        """采集设备接口表。返回接口列表，失败返回 []。"""
        try:
            name_map = self.get_oid(OIDS.IF_NAME, dev)
            oper_map = self.get_oid(OIDS.IF_OPER, dev)
            if not name_map:
                return []
            ifaces = []
            # 解析 ifIndex 从 name OID 尾部
            for oid, name in name_map.items():
                idx = oid.rsplit(".", 1)[-1]
                oper = oper_map.get(OIDS.IF_OPER + "." + idx, "1")
                ifaces.append({
                    "if_index": int(idx) if idx.isdigit() else 0,
                    "if_name": name,
                    "if_oper_status": "up" if oper == "1" else "down",
                })
            return ifaces
        except Exception:
            return []

    def collect_device(self, dev: dict) -> dict:
        """采集单个设备，返回 {sys_descr, interfaces}。降级返回空结构。"""
        result = {"device_id": dev.get("id"), "sys_descr": "", "interfaces": []}
        try:
            descr = self.get_oid(OIDS.SYS_DESCR, dev)
            result["sys_descr"] = descr.get(OIDS.SYS_DESCR, "")
            result["interfaces"] = self._collect_interfaces(dev)
        except Exception:
            pass
        return result

    def collect_all(self) -> None:
        """遍历 active SNMP 设备并采集（可降级，不抛异常）。"""
        try:
            import db
            from db_snmp import SNMPDeviceStore

            if not db.db_available():
                return
            devices = SNMPDeviceStore().list(active_only=True)
            for dev in devices:
                try:
                    data = self.collect_device(dev)
                    if data["interfaces"]:
                        SNMPDeviceStore().save_interfaces(dev["id"], data["interfaces"])
                    SNMPDeviceStore().touch_collect(dev["id"])
                except Exception:
                    continue
        except Exception:
            return

    # ── 调度 ──────────────────────────────────────────
    async def run_forever(self):
        """定时轮询调度。首次立即执行，之后按间隔。"""
        self._running = True
        while self._running:
            try:
                self.collect_all()
            except Exception:
                pass
            await asyncio.sleep(_COLLECT_INTERVAL)
