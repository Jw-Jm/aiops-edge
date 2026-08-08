# 二期强化：SNMP 监控 + Agent 运维工具全量

**日期**: 2026-08-08
**范围**: ai-orchestrator（Python, SNMP 采集 + agent 工具扩展）+ observability-frontend（React）+ MySQL

## 1. 范围与决策（已确认）

| 块 | 决策 |
|----|------|
| SNMP 采集器 | **Python pysnmp**，ai-orchestrator 内实现，定时轮询 |
| SNMP 存储 | **MySQL**（设备/接口/指标，与 P1b 一致）|
| SNMP 验证 | **可降级**（无设备不阻塞）+ **snmpsim 模拟器**测全链路 |
| SNMP 凭据 | 不落库（参照 ongrid），存凭据名，实际密码从配置读取 |
| Agent 工具 | 补齐**工具元数据模型**（Class 三级/Scope/WhenToUse/Origin）+ **网络设备查询工具** |
| IM 通道 | 跳过（用户已确认）|

**借鉴 ongrid 概念（不复制代码）**：
- SNMP：只读 probe、OID 表、设备/接口数据模型、凭据不落库
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

## 3. Agent 运维工具全量

### 3.1 工具元数据模型扩展（参照 ongrid）

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

### 3.2 新增网络设备查询工具

在 `flow_engine/nodes_aiops.py` 或工具注册处新增：
- `snmp_query`：查设备接口/流量
- `snmp_health`：查设备 CPU/内存负载（若有）

### 3.3 工具列表 + 权限映射

工具注册时带 `cls`，前端 Skills 页展示 Class 标签，审批流按 Class 拦截。

---

## 4. 依赖

- ai-orchestrator：`pysnmp`、`snmpsim`（仅测试）
- 前端：无新增（复用 AntD/echarts）
- MySQL：2 张新表（snmp_devices / network_interfaces）

---

## 5. 测试

- **Python**：`test_snmp_collector.py`（mock OID 响应 + 降级）、`test_tool_metadata.py`（工具 Class/Scope 元数据）
- **集成**：snmpsim 模拟设备 → 采集器轮询 → 数据落库 → API 返回
- **前端**：`tsc --noEmit` + `npm run build`
- **冒烟**：添加 SNMP 设备 → 手动采集 → 接口列表

---

## 6. 风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| 无真实 SNMP 设备 | 高 | 中 | snmpsim 模拟 + 可降级 |
| pysnmp 版本兼容 | 中 | 中 | 固定版本 + mock 单测 |
| 凭据安全 | 低 | 高 | 不落库，配置读取 |
| 工具 Class 改造破坏现有 | 中 | 中 | 保留 readonly 兼容，新增 cls 字段 |

---

## 7. 自审

- [x] 无 TBD/TODO
- [x] 范围聚焦：SNMP 完整 + agent 工具扩展（IM 跳过）
- [x] 借鉴 ongrid 概念（OID 表/工具 Class/Scope），不复制代码
- [x] 降级安全：无设备不阻塞
- [x] 凭据不落库
