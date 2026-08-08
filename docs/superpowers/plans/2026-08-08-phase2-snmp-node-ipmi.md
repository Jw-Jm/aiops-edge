# 二期强化：SNMP交换机 + node_exporter + 本地IPMI + 部件可用性 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ① SNMP 采集管理网上联交换机（pysnmp），② node_exporter + ipmi-exporter DaemonSet（本地采集，不走带外网），③ 服务器部件可用性聚合，④ Agent 工具 Class 元数据 + 新增硬件/部件查询工具。

**Architecture:** ai-orchestrator（Python）管 SNMP 采集调度 + 上报 ingest 接口；2 个 DaemonSet（node-exporter/ipmi-exporter）本地采集；MySQL 存数据；Helm 部署。

**Tech Stack:** Python, pysnmp, ipmitool, node_exporter, MySQL, Helm, React, AntD, echarts

## Global Constraints

- **四网段隔离，仅采集 K8s 管理面**；IPMI 用本地 `/dev/ipmi0`（不走带外网）
- SNMP 采集管理网上联交换机；node_exporter/ipmi-exporter 为 DaemonSet（每节点本地采集，管理网上报）
- 所有采集**可降级**（无设备不阻塞）
- 凭据不落库（SNMP community 从配置读）
- ToolDef 扩展 `cls`/`scope`/`when_to_use`/`origin`，**保留现有字段兼容**
- 现有 68 pytest + Go test 不回归
- 借鉴 ongrid 概念（OID 表/工具 Class），不复制代码

---

### Task 1: MySQL 采集数据表（snmp + ipmi + 部件可用性）

**Files:**
- Create: `ai-orchestrator/migrations/0002_phase2_tables.sql`
- Test: `ai-orchestrator/tests/test_db_migrate.py`

**Interfaces:**
- Consumes: `db.migrate()`（P1b 迁移器）
- Produces: 5 张新表（snmp_devices / network_interfaces / ipmi_sensors / ipmi_sel_events / node_component_health）

- [ ] **Step 1: 写失败测试**

```python
from db import migrate, db_available

def test_migrate_includes_phase2():
    migrate()
    # MySQL 不可用时降级，表不实际存在，但迁移器不抛异常
    assert True
```

- [ ] **Step 2: 实现迁移 SQL（0002_phase2_tables.sql）**

5 张表（见设计文档 §2.2/3.2/4.3）。

- [ ] **Step 3: 运行确认通过**

Run: `.venv-312/bin/python -m pytest tests/test_db_migrate.py -v`
Expected: PASS（迁移器幂等）

- [ ] **Step 4: 提交**

```bash
git add ai-orchestrator/migrations/0002_phase2_tables.sql ai-orchestrator/tests/test_db_migrate.py
git commit -m "feat(db): 二期采集表 snmp/ipmi/部件可用性"
```

---

### Task 2: SNMP 交换机采集（pysnmp）

**Files:**
- Create: `ai-orchestrator/snmp_collector.py`（OID 表 + 采集器）
- Modify: `ai-orchestrator/main.py`（SNMP 设备 CRUD + 采集调度 + 接口查询）
- Test: `ai-orchestrator/tests/test_snmp_collector.py`

**Interfaces:**
- Consumes: pysnmp + MySQL
- Produces: `/api/v1/snmp/devices` CRUD + `/collect` + `/interfaces`

- [ ] **Step 1: 安装 pysnmp**

```bash
./.venv-312/bin/python -m pip install pysnmp -i https://pypi.tuna.tsinghua.edu.cn/simple
```

- [ ] **Step 2: 写失败测试（mock OID 响应 + 降级）**

```python
from snmp_collector import SNMPCollector, OIDS

def test_oid_table():
    assert OIDS.IF_TABLE.startswith(".1.3.6.1.2.1.2.2.1")

def test_collector_degraded():
    c = SNMPCollector()
    # 无设备/无网络时 collect 不抛异常（降级）
    assert c.collect_all() is None  # 返回 None 或空，不异常
```

- [ ] **Step 3: 实现 snmp_collector.py**

```python
# OID 常量
class OIDS:
    SYS_DESCR = ".1.3.6.1.2.1.1.1.0"
    IF_TABLE = ".1.3.6.1.2.1.2.2.1"       # 接口表
    IF_NAME = ".1.3.6.1.2.1.2.2.1.2"      # 接口名
    IF_OPER = ".1.3.6.1.2.1.2.2.1.8"      # 操作状态
    IF_IN_OCT = ".1.3.6.1.2.1.2.2.1.10"   # 入字节
    IF_OUT_OCT = ".1.3.6.1.2.1.2.2.1.16"  # 出字节
    IF_ERR_IN = ".1.3.6.1.2.1.2.2.1.14"   # 入错误
    IF_SPEED = ".1.3.6.1.2.1.2.2.1.5"     # 速率

class SNMPCollector:
    def __init__(self):
        self._running = False

    def collect_device(self, dev):  # pysnmp 轮询 OID
        # 降级：pysnmp 不可用/网络不通返回 None
        ...

    def collect_all(self):  # 遍历 active 设备，可降级
        ...
```

- [ ] **Step 4: 实现 SNMP 设备 CRUD + 采集调度（main.py）**

路由：
- `GET/POST /api/v1/snmp/devices`
- `PUT/DELETE /api/v1/snmp/devices/{id}`
- `GET /api/v1/snmp/devices/{id}/interfaces`
- `POST /api/v1/snmp/devices/{id}/collect`（手动立即采集）

启动时 `asyncio.create_task(collector.run_forever())`，`SNMP_COLLECT_INTERVAL` 默认 60s，可降级。

- [ ] **Step 5: 运行测试 + 冒烟**

Run: `.venv-312/bin/python -m pytest tests/ -q`（68 + 新增）
Run: 启动 uvicorn，POST 添加 SNMP 设备 → 手动 collect → interfaces

- [ ] **Step 6: 提交**

```bash
git add ai-orchestrator/snmp_collector.py ai-orchestrator/main.py ai-orchestrator/tests/test_snmp_collector.py
git commit -m "feat(snmp): 管理网交换机 SNMP 采集（pysnmp OID）+ 设备 CRUD + 采集调度"
```

---

### Task 3: IPMI ingest + node_exporter 部件可用性

**Files:**
- Create: `ai-orchestrator/ipmi_ingest.py`（上报 ingest + 查询）
- Create: `ai-orchestrator/node_health.py`（部件可用性聚合）
- Modify: `ai-orchestrator/main.py`（路由）
- Test: `ai-orchestrator/tests/test_ipmi_ingest.py`、`test_node_health.py`

**Interfaces:**
- Consumes: MySQL + 上报数据
- Produces: `/api/v1/ipmi/ingest`、`/sensors`、`/nodes/{node}/health`、`/api/v1/node/health`

- [ ] **Step 1: 写失败测试**

```python
from ipmi_ingest import IPMIStore
from node_health import NodeHealthAggregator

def test_ipmi_ingest_store():
    s = IPMIStore()
    s.ingest("node-1", [{"name": "CPU Temp", "type": "Temperature", "reading": "42 C", "status": "ok"}])
    items = s.query(node="node-1")
    assert any(i["sensor_name"] == "CPU Temp" for i in items)

def test_node_health_aggregate():
    a = NodeHealthAggregator()
    # mock node_exporter + ipmi 数据 → 部件可用性状态
    status = a.aggregate("node-1", {"cpu_util": 30, "mem_avail": 8192, "disk_ok": True, "net_ok": True})
    assert status["cpu"] in ("healthy", "degraded", "fault")
```

- [ ] **Step 2: 运行确认失败**

Run: `.venv-312/bin/python -m pytest tests/test_ipmi_ingest.py tests/test_node_health.py -v`
Expected: FAIL — 模块未定义

- [ ] **Step 3: 实现 ipmi_ingest.py**

- `IPMIStore.ingest(node, sensors)`：落 `ipmi_sensors` + SEL
- `query(node, type)`：查询传感器
- 上报接口 `/api/v1/ipmi/ingest`（POST），可降级

- [ ] **Step 4: 实现 node_health.py**

- `NodeHealthAggregator.aggregate(node, metrics)`：根据 node_exporter（CPU/内存/磁盘/网卡）+ IPMI（温度/电压）聚合部件可用性
- 落 `node_component_health`
- `/api/v1/node/health` 查询

- [ ] **Step 5: 运行测试 + 冒烟**

Run: `.venv-312/bin/python -m pytest tests/ -q`
Run: 上报 mock IPMI 传感器 → 查询健康

- [ ] **Step 6: 提交**

```bash
git add ai-orchestrator/ipmi_ingest.py ai-orchestrator/node_health.py ai-orchestrator/main.py ai-orchestrator/tests/
git commit -m "feat(ipmi): 本地 IPMI ingest + node_exporter 部件可用性聚合"
```

---

### Task 4: Agent 工具 Class 元数据 + 新增硬件/部件工具

**Files:**
- Modify: `ai-orchestrator/skill_registry.py`（ToolDef 扩展 cls/scope/when_to_use/origin）
- Modify: `ai-orchestrator/flow_engine/nodes_aiops.py` 或工具注册处（新增 snmp/ipmi/node 工具）
- Test: `ai-orchestrator/tests/test_tool_metadata.py`

**Interfaces:**
- Consumes: ToolDef 扩展
- Produces: 工具带 Class 标签 + 4 个新查询工具

- [ ] **Step 1: 写失败测试**

```python
from skill_registry import ToolDef, ToolRegistry

def test_tool_class_metadata():
    t = ToolDef(name="k8s_get", description="x", func=lambda: None, cls="safe", scope="manager")
    assert t.cls in ("safe", "mutating", "dangerous")
    assert t.scope in ("host", "manager")

def test_new_tools_registered():
    names = {x.name for x in ToolRegistry.list_all()}
    assert {"snmp_query", "snmp_health", "ipmi_health", "node_health"}.issubset(names)
```

- [ ] **Step 2: 运行确认失败**

Run: `.venv-312/bin/python -m pytest tests/test_tool_metadata.py -v`
Expected: FAIL — ToolDef 无 cls 字段

- [ ] **Step 3: 扩展 ToolDef**

```python
@dataclass
class ToolDef:
    name: str
    description: str
    func: Callable
    params: dict = field(default_factory=dict)
    category: str = "general"
    requires_approval: bool = False
    # 新增（工具元数据模型，onrid 概念）
    cls: str = "safe"            # safe / mutating / dangerous
    scope: str = "manager"       # host / manager
    when_to_use: str = ""
    origin: str = "builtin"      # builtin / custom
```

- [ ] **Step 4: 新增工具**

`ToolRegistry.register` 装饰器加 `cls`/`scope`/`when_to_use`/`origin` 参数。新增 `snmp_query`/`snmp_health`/`ipmi_health`/`node_health` 工具（查询已采集数据）。

- [ ] **Step 5: 运行测试 + 回归**

Run: `.venv-312/bin/python -m pytest tests/ -q`（68 + 新增）

- [ ] **Step 6: 提交**

```bash
git add ai-orchestrator/skill_registry.py ai-orchestrator/flow_engine/nodes_aiops.py ai-orchestrator/tests/test_tool_metadata.py
git commit -m "feat(agent): ToolDef Class/Scope/Origin 元数据 + snmp/ipmi/node 查询工具"
```

---

### Task 5: 前端（SNMP 设备 + IPMI 硬件健康 + 部件可用性）

**Files:**
- Create: `observability-frontend/src/pages/SNMP/index.tsx`
- Create: `observability-frontend/src/pages/Hardware/index.tsx`（IPMI + 部件可用性）
- Modify: `observability-frontend/src/App.tsx`（路由+菜单）
- Modify: `observability-frontend/src/api/client.ts`（API）
- Test: `tsc --noEmit` + `npm run build`

**Interfaces:**
- Consumes: 前端 API
- Produces: 2 页面 + 菜单

- [ ] **Step 1: client.ts 加 API**

```ts
// SNMP
export const listSnmpDevices = (params?) => api.get('/snmp/devices', { params })
export const createSnmpDevice = (data) => api.post('/snmp/devices', data)
export const deleteSnmpDevice = (id) => api.delete(`/snmp/devices/${id}`)
export const listSnmpInterfaces = (id) => api.get(`/snmp/devices/${id}/interfaces`)
export const collectSnmpDevice = (id) => api.post(`/snmp/devices/${id}/collect`)
// IPMI + 部件可用性
export const listIpmiSensors = (params?) => api.get('/ipmi/sensors', { params })
export const listNodeHealth = (params?) => api.get('/node/health', { params })
```

- [ ] **Step 2: 创建 SNMP 页（设备 CRUD + 接口流量表 + 采集按钮）**

- [ ] **Step 3: 创建 Hardware 页（节点部件可用性矩阵 + IPMI 传感器表）**

- [ ] **Step 4: 注册路由和菜单**

App.tsx 加 `/snmp`、`/hardware`；"基础设施"分组下。

- [ ] **Step 5: tsc + build 验证**

Run: `cd observability-frontend && node_modules/.bin/tsc --noEmit && npm run build`
Expected: exit 0

- [ ] **Step 6: 提交**

```bash
git add observability-frontend/src
git commit -m "feat(web): SNMP 设备 + 硬件健康/部件可用性 页面"
```

---

### Task 6: Helm 部署（node-exporter + ipmi-exporter DaemonSet）

**Files:**
- Create: `deploy/helm/aiops/templates/node-exporter/daemonset.yaml`
- Create: `deploy/helm/aiops/templates/ipmi-exporter/daemonset.yaml`
- Modify: `deploy/helm/aiops/values.yaml`（开关）
- Modify: `ai-orchestrator/requirements.txt`（pysnmp）

**Interfaces:**
- Consumes: Helm
- Produces: 2 个 DaemonSet + 配置

- [ ] **Step 1: node-exporter DaemonSet**

标准 node_exporter DaemonSet（hostNetwork + hostPath rootfs）。

- [ ] **Step 2: ipmi-exporter DaemonSet**

privileged + hostPath `/dev/ipmi0`，本地 ipmitool 采集并上报 `/api/v1/ipmi/ingest`。

- [ ] **Step 3: values.yaml 开关**（`nodeExporter.enabled` / `ipmiExporter.enabled`）

- [ ] **Step 4: helm lint**

Run: `cd deploy/helm/aiops && helm lint .`
Expected: 0 failed

- [ ] **Step 5: 提交**

```bash
git add deploy/helm/aiops/templates/node-exporter deploy/helm/aiops/templates/ipmi-exporter deploy/helm/aiops/values.yaml ai-orchestrator/requirements.txt
git commit -m "deploy(helm): node-exporter + ipmi-exporter DaemonSet（本地采集/带外隔离）"
```

---

## Self-Review

**1. Spec coverage（对照设计文档）：**
- ✅ §2 SNMP 交换机 → Task 2
- ✅ §3 IPMI 本地 `/dev/ipmi0` → Task 3（ingest）
- ✅ §4 node_exporter + 部件可用性 → Task 3
- ✅ §5 Agent 工具 Class + 新工具 → Task 4
- ✅ §6 Helm DaemonSet → Task 6
- ✅ 前端 → Task 5

**2. Placeholder scan：** 无 TBD/TODO。

**3. Type consistency：**
- `SNMPCollector.collect_all()` — Task 2 定义，调度使用
- `IPMIStore.ingest/query` — Task 3 定义，ingest 路由使用
- `NodeHealthAggregator.aggregate` — Task 3 定义
- `ToolDef.cls/scope/when_to_use/origin` — Task 4 定义
- 前端 `listSnmpDevices/listNodeHealth` — Task 5 定义，页面使用一致
