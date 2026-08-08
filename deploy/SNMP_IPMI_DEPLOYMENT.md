# SNMP + IPMI 采集部署与生产配置指南

> 四网段隔离环境下（带外网 / 管理网 / 业务网 / 存储网 完全隔离），仅采集 K8s 管理面资源。
> 本文档说明 **SNMP 交换机采集** 与 **IPMI 服务器硬件采集** 的上生产配置方式。

---

## 1. 架构概览

```
┌─────────────── K8s 集群（管理网） ───────────────┐
│  ai-orchestrator                                 │
│   ├─ SNMP 采集器（pysnmp）→ 管理网上联交换机      │
│   ├─ /api/v1/ipmi/ingest（接收 IPMI 上报）       │
│   └─ MySQL（snmp_devices/network_interfaces/    │
│        ipmi_sensors/ipmi_sel_events/            │
│        node_component_health）                   │
│                                                  │
│  node-exporter (DaemonSet) → 服务器 OS 部件       │
│  ipmi-exporter (DaemonSet) → 本地 /dev/ipmi0 BMC │
└──────────────────────────────────────────────────┘
```

**采集通道约束**：
- **SNMP**：采集器在管理网，通过管理网访问**上联交换机**（SNMP 只读）
- **IPMI**：**不依赖带外网**！ipmi-exporter DaemonSet 在**每台服务器本地**用 `ipmitool` 读 `/dev/ipmi0`（BMC 本地 KCS 接口），结果经管理网上报

---

## 2. SNMP 交换机采集配置

### 2.1 前置条件（交换机侧）

在管理网上联交换机上开启 **SNMP 只读**（v2c 或 v3）：

```bash
# 以 Cisco IOS 示例（只读 community，只允许管理网网段访问）
snmp-server community <READ_COMMUNITY> RO 99
access-list 99 permit <K8S_MANAGEMENT_CIDR>   # 只允许采集器所在网段

# 或 v3 更安全
snmp-server group AIOPS_READ v3 priv read AIOPS_VIEW
snmp-server user aiops <GROUP> v3 auth sha <AUTH_PASS> priv aes <PRIV_PASS>
snmp-server view AIOPS_VIEW iso included
```

> **安全提示**：SNMP 只读，绝不开启 RW；community 只允许管理网 CIDR 访问。

### 2.2 采集器配置（ai-orchestrator 环境变量）

| 环境变量 | 默认 | 说明 |
|---------|------|------|
| `SNMP_COLLECT_INTERVAL` | `60` | 轮询间隔（秒）|
| `SNMP_TIMEOUT` | `3` | 单次 SNMP 超时（秒）|
| `SNMP_COMMUNITY` | `public` | 默认只读 community |

### 2.3 添加设备（界面 / API）

**方式一：前端 SNMP 页面**（`/snmp`）→ "添加设备" 表单，填 IP/community/厂商。

**方式二：API**：

```bash
# 添加管理网上联交换机
curl -X POST http://<ai-orchestrator>/api/v1/snmp/devices \
  -H "Content-Type: application/json" \
  -d '{"ip":"192.168.10.1","hostname":"sw-core-01","community":"<READ_COMMUNITY>","vendor":"Cisco"}'

# 手动立即采集
curl -X POST http://<ai-orchestrator>/api/v1/snmp/devices/1/collect

# 查看接口
curl http://<ai-orchestrator>/api/v1/snmp/devices/1/interfaces
```

### 2.4 生产排障

| 现象 | 排查 |
|------|------|
| 采集接口为空 | 检查交换机 community/ACL 是否允许管理网；`curl /collect` 看返回 |
| 超时 | 确认交换机管理 IP 在管理网可达；`SNMP_TIMEOUT` 调大 |
| 设备不可达 | SNMP 走管理网，确认路由；上联交换机需能 ping 通 |

---

## 3. IPMI 服务器硬件采集配置

### 3.1 前置条件（服务器侧）

服务器主板需开启 IPMI（BMC），且 **`/dev/ipmi0` 设备可用**（内核 ipmi_si 驱动加载）：

```bash
# 服务器本机检查
ls -l /dev/ipmi0            # 存在则为本地 KCS 接口可用
ipmitool sensor list        # 能读传感器（测试用）
```

> 本方案用**本地 KCS 接口**（`/dev/ipmi0`），**不需要** BMC 带外网 IP 可达。这正解决"带外网隔离"问题。

### 3.2 镜像构建与部署

**构建镜像**（本地 tag，与 K8s 内镜像一致）：

```bash
./deploy/scripts/build-images.sh ipmi    # 构建 ipmi-exporter:latest
```

> **镜像分发说明**：本地 OrbStack K8s 对**无 registry 前缀的本地镜像**拉取受限（`ipmi-exporter:latest` 会被解析到 docker.io 而失败）。因此：
> - 本机开发默认 `ipmiExporter.enabled: false`（避免 ImagePullBackOff）
> - **生产/真实 K8s 集群**：把镜像 push 到集群可访问的 registry（如 `registry.example.com/ipmi-exporter:latest`），修改 `values.yaml` 的 `image` 后再启用
> ```bash
> docker tag ipmi-exporter:latest registry.example.com/ipmi-exporter:latest
> docker push registry.example.com/ipmi-exporter:latest
> ```

**Helm 部署开关**（`deploy/helm/aiops/values.yaml`）：

```yaml
nodeExporter:
  enabled: true          # 每节点采 OS 部件（CPU/内存/磁盘/网卡）
  image: "prom/node-exporter:v1.8.2"
ipmiExporter:
  enabled: false         # 生产环境 push 镜像到 registry 后改 true
  image: "ipmi-exporter:latest"   # 生产改为 registry 完整路径
  collectInterval: "120" # 采集间隔秒
```

**应用**：
```bash
./deploy/scripts/apply.sh   # 或 helm upgrade aiops deploy/helm/aiops
```

### 3.3 ipmi-exporter DaemonSet 说明

- **privileged** + **hostPath `/dev/ipmi0`**（只读）访问本地 BMC
- 用 `ipmitool sensor list` 读温度/风扇/电压/电源，`ipmitool sel list` 读事件
- 每 `COLLECT_INTERVAL` 秒经管理网 POST 到 `ai-orchestrator:8000/api/v1/ipmi/ingest`
- **降级**：节点无 `/dev/ipmi0`（非服务器/驱动未加载）时跳过采集，不阻塞

### 3.4 生产排障

| 现象 | 排查 |
|------|------|
| 某节点无传感器 | 该节点 `/dev/ipmi0` 不可用（非物理服务器或 ipmi_si 未加载）；查 ipmi-exporter 日志 "no ipmi devices" |
| 全部节点无数据 | 检查 DaemonSet 是否 Running；privileged + hostPath 是否生效 |
| ingest 失败 | 检查 ai-orchestrator `/api/v1/ipmi/ingest` 可达（管理网内 Service DNS）|

---

## 4. 数据落库与查看

| 表 | 内容 | 前端页 |
|----|------|--------|
| `snmp_devices` / `network_interfaces` | SNMP 交换机 + 接口流量 | `/snmp` |
| `ipmi_sensors` / `ipmi_sel_events` | IPMI 传感器 + SEL 事件 | `/hardware` |
| `node_component_health` | 服务器部件可用性（CPU/内存/磁盘/网卡）| `/hardware` |

**Agent 查询工具**（AI 诊断可调用）：
- `snmp_query` / `snmp_health` — 网络设备
- `ipmi_health` — 服务器硬件健康
- `node_health` — 服务器部件可用性

---

## 5. 四网段隔离注意事项

| 网段 | 本方案采集方式 | 是否采集 |
|------|--------------|---------|
| 带外网（IPMI 远程）| **不采集**（隔离）| ❌ |
| 管理网（SSH+K8s）| SNMP 采上联交换机 + node/ipmi 本地采集上报 | ✅ |
| 业务网（kubevirt）| 不采集 | ❌ |
| 存储网 | 不采集 | ❌ |

> 若未来网络打通（管理网→带外网路由），可在 `ipmi_exporter` 或 ai-orchestrator 增加**远程 IPMI** 采集（`ipmitool -H <bmc_ip>`），但当前隔离环境下**推荐本地 `/dev/ipmi0`**。
