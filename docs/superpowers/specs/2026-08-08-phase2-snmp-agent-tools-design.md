# 二期强化：SNMP（管理网交换机）+ 服务器部件可用性（node_exporter + 本地IPMI）+ Agent 工具全量

**日期**: 2026-08-08
**范围**: ai-orchestrator（Python 采集调度）+ observability-frontend（React）+ MySQL + K8s Helm（采集器 DaemonSet）

## 0. 网络拓扑约束（生产环境）

**服务器四网段完全隔离，仅采集 K8s 管理面**：
| 网段 | 用途 | 采集方式 |
|------|------|---------|
| 带外网 | IPMI BMC 远程管理 | **不可达**（隔离），不远程采集 |
| 管理网 | 服务器 SSH + K8s 管理面 | **采集器所在**，SNMP 采上联交换机 |
| 业务网 | kubevirt VM / pod / service | 隔离，本次不采集 |
| 存储网 | 存储 | 隔离，本次不采集 |

**核心方案**（解决带外不通）：
- **SNMP** → 采集管理网上联交换机（管理网可达）
- **node_exporter**（DaemonSet）→ 服务器 OS 部件（CPU/内存/磁盘/网卡），本地采集管理网上报
- **IPMI 本地**（DaemonSet + hostPath `/dev/ipmi0` + privileged）→ `ipmitool` 本地读 BMC 传感器（**不走带外网**）
- **部件可用性聚合**：聚合 node_exporter + IPMI 数据，给出 CPU/内存/磁盘/网卡可用性状态

## 1. 范围与决策（已确认）

| 块 | 决策 |
|----|------|
| SNMP 采集器 | **Python pysnmp**，管理网上联交换机，定时轮询 |
| IPMI 采集器 | **本地 ipmitool**（DaemonSet 挂 hostPath `/dev/ipmi0` + privileged），不走带外网 |
| 服务器部件 | **node_exporter**（DaemonSet），CPU/内存/磁盘/网卡 OS 层 |
| 部件可用性 | 聚合 node_exporter + IPMI，给出各部件可用性状态 |
| 存储 | **MySQL**（snmp_devices / network_interfaces / ipmi_sensors / node_metrics / 部件可用性）|
| 验证 | **可降级**（无设备不阻塞）+ **snmpsim 模拟器**（SNMP）+ **mock 单测**（IPMI/node_exporter）|
| 凭据 | 不落库（SNMP community），从配置读取 |
| Agent 工具 | 补齐**工具元数据模型**（Class 三级/Scope/WhenToUse/Origin）+ **网络设备 + 硬件健康查询工具** |
| IM 通道 | 跳过（用户已确认）|

**借鉴 ongrid 概念（不复制代码）**：
- SNMP：只读 probe、OID 表、设备/接口数据模型、凭据不落库
- IPMI：本地 KCS 接口（`/dev/ipmi0`）传感器轮询
- 工具：Class(safe/mutating/dangerous) 三级、Scope(host/manager)、WhenToUse、Origin

---

## 2. SNMP 设计

### 2.1 数据模型（MySQL 新表，query-api 或 orchestrator 迁移）

参照 ongrid，SNMP 数据模型：

```sql
-- SNMP 设备（关联 devices 或独立）
CREATE TABLE IF NOT EXISTS snmp_devices (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  hostname VARCHAR(128) NOT NULL,
  ip VARCHAR(64) NOT NULL UNIQUE,
  community VARCHAR(64) DEFAULT 'public',    -- 只读 community
  snmp_version ENUM('v2c','v3') DEFAULT 'v2c',
  vendor VARCHAR(64) DEFAULT '',             -- 厂商
  model VARCHAR(64) DEFAULT '',              -- 型号
  location VARCHAR(128) DEFAULT '',
  status ENUM('active','disabled') DEFAULT 'active',
  last_collect_at DATETIME NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 网络接口（每设备多接口）
CREATE TABLE IF NOT EXISTS network_interfaces (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  device_id BIGINT NOT NULL,
  if_index INT,
  if_name VARCHAR(128),
  if_oper_status VARCHAR(32),
  if_admin_status VARCHAR(32),
  if_speed_mbps BIGINT DEFAULT 0,
  if_in_octets BIGINT DEFAULT 0,
  if_out_octets BIGINT DEFAULT 0,
  if_in_errors BIGINT DEFAULT 0,
  if_out_errors BIGINT DEFAULT 0,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_device (device_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 2.2 采集器（ai-orchestrator, Python）

`snmp_collector.py`：
- **OID 表**（只读指标）：
  - `sysDescr` `.1.3.6.1.2.1.1.1.0`（设备描述）
  - 接口表 `ifTable` `.1.3.6.1.2.1.2.2.1`（ifName/ifOperStatus/ifInOctets/ifOutOctets/ifInErrors/ifSpeed）
  - CPU 负载 `1.3.6.1.4.1.2021.11.9.0`（UCD-SNMP 负载，可选）
- **轮询**：`asyncio` 定时任务，每 `SNMP_COLLECT_INTERVAL`（默认 60s）遍历 active 设备
- **降级**：无设备（`snmp_devices` 空）或 pysnmp 不可用时跳过，不阻塞服务
- **写库**：采集结果 upsert 到 `network_interfaces`

```python
# snmp_collector.py
class SNMPCollector:
    def __init__(self):
        self._running = False
        self._devices = None

    async def collect_device(self, dev):  # 用 pysnmp 轮询 OID
        ...

    async def run_forever(self):  # 定时调度，降级安全
        while self._running:
            try:
                await self.collect_all()
            except Exception:
                pass
            await asyncio.sleep(interval)
```

### 2.3 API 路由（ai-orchestrator/main.py）

| 端点 | 方法 | 逻辑 |
|------|------|------|
| `/api/v1/snmp/devices` | GET | SNMP 设备列表 |
| `/api/v1/snmp/devices` | POST | 添加设备（凭据名，不存密码明文）|
| `/api/v1/snmp/devices/{id}` | PUT/DELETE | 编辑/删除 |
| `/api/v1/snmp/devices/{id}/interfaces` | GET | 设备接口 + 流量指标 |
| `/api/v1/snmp/devices/{id}/collect` | POST | 手动立即采集 |

### 2.4 前端（React）

`/snmp` 页：
- 设备列表（IP/厂商/状态/最后采集时间）
- 添加设备表单
- 设备详情 → 接口流量表（收发字节/错误/速率）
- 接口流量趋势（echarts，可选）

---

## 3. IPMI 服务器硬件监控（本地 `/dev/ipmi0`，不走带外网）

### 3.1 采集方式（关键：本地 KCS 接口）

IPMI 除网络通道（RMCP，走带外网）外，还支持**本地 KCS 接口** `(/dev/ipmi0)`——服务器主板上的 BMC 芯片驱动，**不需要网络**。

**方案**：`ipmi-exporter` DaemonSet（privileged 容器）挂载 hostPath `/dev/ipmi0`，在**每台服务器本地**用 `ipmitool` 读 BMC 传感器，采集结果经管理网上报中心。

```yaml
# ipmi-exporter DaemonSet（每 K8s 节点一台）
spec:
  hostNetwork: true
  containers:
    - name: ipmi-exporter
      image: ...ipmi-exporter:latest
      securityContext:
        privileged: true          # 需要访问 /dev/ipmi0
      volumeMounts:
        - name: ipmi
          mountPath: /dev/ipmi0
          readOnly: true
      volumes:
        - name: ipmi
          hostPath:
            path: /dev/ipmi0     # hostPath 挂载宿主机 IPMI KCS 设备
```

采集逻辑：`ipmitool sensor`（读温度/风扇/电压/电源）+ `ipmitool sel`（系统事件日志），上报到中心入库。

### 3.2 数据模型（MySQL 新表）

```sql
-- IPMI 传感器（温度/风扇/电压/电源）— 按节点采集
CREATE TABLE IF NOT EXISTS ipmi_sensors (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  node_name VARCHAR(128) NOT NULL,           -- K8s 节点名（关联设备）
  sensor_name VARCHAR(128),
  sensor_type VARCHAR(64),                   -- Temperature/Fan/Voltage/Power/Health
  reading VARCHAR(64),                       -- 读数（含单位，如 "42 C"）
  status VARCHAR(32),                        -- ok / warning / critical
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_node (node_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- IPMI 系统事件日志（SEL）
CREATE TABLE IF NOT EXISTS ipmi_sel_events (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  node_name VARCHAR(128) NOT NULL,
  event_id VARCHAR(64),
  event_time DATETIME,
  sensor VARCHAR(128),
  event_desc VARCHAR(255),
  severity VARCHAR(16),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  KEY idx_node_time (node_name, event_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 3.3 采集上报

- ipmi-exporter 采集传感器 → 上报 `/api/v1/ipmi/ingest`（orchestrator）→ 落 `ipmi_sensors` + `ipmi_sel_events`
- 上报接口**可降级**：无 ipmi-exporter 上报时无数据，不阻塞
- `IPMI_COLLECT_INTERVAL` 默认 120s（ipmi-exporter 侧轮询频率）

### 3.4 API 路由（ai-orchestrator/main.py）

| 端点 | 方法 | 逻辑 |
|------|------|------|
| `/api/v1/ipmi/ingest` | POST | ipmi-exporter 上报传感器（节点/类型/读数/状态）|
| `/api/v1/ipmi/sensors` | GET | 查询传感器（按节点/类型过滤）|
| `/api/v1/ipmi/nodes/{node}/health` | GET | 节点硬件健康汇总 |

### 3.5 前端（React）

`/ipmi` 页：
- 节点列表 + 硬件健康状态（温度/风扇/电压/电源）
- 传感器表格（按类型分组，状态 Tag）
- SEL 事件列表

---

## 4. 服务器部件可用性（node_exporter + IPMI 聚合）

### 4.1 node_exporter（DaemonSet，OS 层部件）

部署标准 `node_exporter` DaemonSet，采集服务器 OS 部件指标：

| 部件 | 指标 | 可用性判断 |
|------|------|-----------|
| CPU | `node_cpu_seconds_total` | 可用（非全部 idle）|
| 内存 | `node_memory_MemAvailable_bytes` | 可用（非 OOM）|
| 磁盘 | `node_filesystem_avail_bytes` | 可用（非只读/满）|
| 网卡 | `node_network_operstate` | 可用（up）|

### 4.2 部件可用性聚合

聚合 node_exporter（OS 部件）+ IPMI（硬件健康）→ 给出**服务器部件可用性状态**：

| 部件 | OS 层（node_exporter）| 硬件层（IPMI）| 聚合状态 |
|------|----------------------|---------------|---------|
| CPU | 使用率/可用 | 温度（若高温→降级）| healthy / degraded / fault |
| 内存 | 可用量/可用率 | 电压 | healthy / degraded / fault |
| 磁盘 | 可用空间/健康 | SEL 磁盘事件 | healthy / degraded / fault |
| 网卡 | 链路 up/down | 接口 | healthy / degraded / fault |

- 存储：`node_component_health` 表（节点/部件/状态/时间）
- 前端 `/nodes` 或 `/ipmi` 页展示部件可用性矩阵

### 4.3 数据模型

```sql
-- 服务器部件可用性
CREATE TABLE IF NOT EXISTS node_component_health (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  node_name VARCHAR(128) NOT NULL,
  component VARCHAR(32),                      -- cpu/memory/disk/network
  status VARCHAR(32),                         -- healthy/degraded/fault
  detail VARCHAR(255),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_node_comp (node_name, component)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 5. Agent 运维工具全量

### 5.1 工具元数据模型扩展（参照 ongrid）

现有 `ToolDef`（name/desc/args/readonly）扩展为：

```python
class ToolDef:
    name: str
    desc: str
    args: dict
    readonly: bool
    # 新增（onrid 工具模型概念）
    cls: str = "safe"          # Class: safe / mutating / dangerous
    scope: str = "manager"     # Scope: host / manager
    when_to_use: str = ""      # WhenToUse: 何时用该工具
    origin: str = "builtin"    # Origin: builtin / custom
```

**Class 三级权限**（映射现有 readonly）：
- `safe`：只读查询（k8s get、日志、指标）
- `mutating`：受控变更（需审批，等同现有 readonly=False 的 write 白名单）
- `dangerous`：危险操作（禁止，除非明确审批）

### 5.2 新增网络设备 + 硬件健康 + 部件查询工具

在 `flow_engine/nodes_aiops.py` 或工具注册处新增：
- `snmp_query`：查网络设备接口/流量
- `snmp_health`：查设备 CPU/内存负载（若有）
- `ipmi_health`：查服务器硬件健康（温度/风扇/电压/电源）
- `node_health`：查服务器部件可用性（CPU/内存/磁盘/网卡）

### 5.3 工具列表 + 权限映射

工具注册时带 `cls`，前端 Skills 页展示 Class 标签，审批流按 Class 拦截。

---

## 6. 部署（Helm）

新增 2 个 DaemonSet（解决带外隔离，本地采集）：
- `node-exporter` DaemonSet（标准，采集 OS 部件）
- `ipmi-exporter` DaemonSet（privileged + hostPath `/dev/ipmi0`，本地读 BMC 传感器）

ai-orchestrator Deployment 保持，新增采集调度与上报 ingest 接口。

---

## 7. 测试

- **Python**：`test_snmp_collector.py`（mock OID 响应 + 降级）、`test_ipmi_ingest.py`（上报→落库→API）、`test_node_health.py`（部件可用性聚合）、`test_tool_metadata.py`（工具 Class/Scope 元数据）
- **集成**：snmpsim 模拟管理网交换机 → SNMP 采集 → 落库 → API；mock node_exporter/IPMI 上报 → 部件可用性聚合
- **前端**：`tsc --noEmit` + `npm run build`
- **冒烟**：添加 SNMP 设备 → 手动采集 → 接口列表；上报 IPMI 传感器 → 硬件健康

---

## 8. 风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| 无真实 SNMP 设备 | 高 | 中 | snmpsim 模拟 + 可降级 |
| 无真实 BMC（`/dev/ipmi0` 不可用）| 中 | 中 | mock 单测 + ipmi-exporter 检测无设备跳过 |
| hostPath `/dev/ipmi0` 权限 | 中 | 中 | privileged 容器 + 只读挂载 |
| node_exporter 重复采集 | 低 | 低 | 与现有指标去重 |
| 工具 Class 改造破坏现有 | 中 | 中 | 保留 readonly 兼容，新增 cls 字段 |

---

## 9. 自审

- [x] 无 TBD/TODO
- [x] 范围聚焦：SNMP 管理网交换机 + node_exporter + 本地 IPMI + 部件可用性（IM 跳过）
- [x] 四网段隔离约束下仅采管理面，IPMI 用本地 `/dev/ipmi0` 解决带外不通
- [x] 借鉴 ongrid 概念（OID 表/工具 Class/Scope），不复制代码
- [x] 降级安全：无设备不阻塞
- [x] 凭据不落库
