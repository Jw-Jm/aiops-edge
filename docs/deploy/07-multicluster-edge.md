# 07 中心平台 → 边缘集群：数据接入组件下发清单

> **定位**：本文档回答"若本平台作为**中心管理平台**（一套中心集群承载管理面+数据底座+智能运维），
> 需要在**其他被纳管 K8s 集群**下发哪些组件，才能完整接入数据，实现物理层→平台层→应用层的
> 全方位监控与智能运维"。
>
> **架构形态**：**集中式**（数据全部汇聚中心，边缘只采集不上报业务状态）；隔离网络场景见 §7。
>
> **前提**：中心集群已部署完整 AIOps 平台（见 03-deploy.md），边缘集群与中心**网络可达**
> （VPN/专线/公网，中心暴露必要端点）。

---

## 1. 中心 vs 边缘 职责划分

```
┌──────────────── 中心集群（管理面 + 数据底座 + 智能运维）────────────────┐
│ frontend │ query-api │ ai-orchestrator │ 图谱 │ 告警 │ 审批 │ 审计       │
│ ClickHouse │ VictoriaMetrics │ VictoriaLogs │ MySQL │ ChromaDB           │
│ 中心侧端点（边缘需可达）: ingest:8080 / vm:8428 / vlogs:9428 / ch:8123   │
└──────────────┬─────────────────────────────────────────────────────────┘
        数据汇聚（边缘 → 中心）
   ┌────────────┼────────────┐
   ▼            ▼            ▼
┌─集群A────┐ ┌─集群B────┐ ┌─集群C────┐
│ 下发组件  │ │ 下发组件  │ │ 下发组件  │
│ (§3 清单) │ │ (§3 清单) │ │ (§3 清单) │
└──────────┘ └──────────┘ └──────────┘
```

**中心保留**：管理面（前端/API/AI/图谱/告警/审批）、全部数据底座（CH/VM/VLogs/MySQL）、
知识库、审计、容量预测、WebShell 等智能与治理能力。

**边缘下发**：仅**采集/上报面**（§3 清单），保证"数据进得来"，业务状态不落边缘。

---

## 2. 三层监控数据接入全景

| 监控层 | 数据类别 | 边缘采集组件 | 上报目标（中心） | 传输 |
|---|---|---|---|---|
| **物理层** | IPMI 传感器/SEL、主机 OS 指标 | categraf（ipmi/sel/cpu/mem/disk/net/system 插件） | VM (remote write) + CH (SEL 经 event-collector) | HTTP |
| **平台层** | K8s 资源/事件、中间件深度指标 | event-collector（K8s 事件）+ categraf（mysql/redis 插件） | CH (k8s_events) + VM | HTTP |
| **应用层** | 服务 RED、trace、应用日志 | ingest（OTLP 接收）+ 日志 shipper | CH (trace/log) + VM (RED) + VLogs | OTLP HTTP / JSON |
| **网络层**（可选） | eBPF 流量/应用性能 | DeepFlow agent | 中心 DeepFlow server（或边缘独立 DeepFlow 栈） | 专有协议 |
| **变更/工单** | 发布事件、复盘案例 | 发布流水线 webhook → 中心 | CH (change_events) + 图谱 | HTTP |

---

## 3. 边缘集群下发组件清单（核心）

### 3.1 必选组件（3 个）

| 组件 | 部署形态 | 采集内容 | 上报目标 | 配置要点 |
|---|---|---|---|---|
| **categraf** | DaemonSet（每节点） | 主机指标（cpu/mem/disk/net/system）+ IPMI（温度/风扇/电源/SEL 计数）+ mysql/redis 中间件插件 | VM remote write：`http://<中心VM地址>:8428/api/v1/write` | ① writers 指向中心 VM ② 全局 labels 注入 `cluster_id=<本集群>` ③ mysql/redis 插件配置指向本集群中间件地址 ④ IPMI 需 privileged+hostPath /dev/ipmi0 |
| **event-collector** | Deployment（单副本，事件集群级）或 DaemonSet（SEL 本地） | K8s 事件（Warning/Error）+ IPMI SEL 明细 | 中心 CH：`http://<中心CH>:8123`（建 k8s_events 表） | ① CH 地址指向中心 ② CLUSTER_ID=<本集群> ③ RBAC：events get/list/watch ④ SEL 需 privileged+ipmitool |
| **ingest** | Deployment（单副本，RWO WAL） | OTLP traces/logs 接收（应用侧 SDK 上报到本集群 ingest） | 中心 CH（trace/log）+ 中心 VM（RED 指标，经自身 /metrics） | ① CLUSTER_ID=<本集群> ② CLICKHOUSE_HOST 指向中心 ③ INGEST_API_KEY 用中心下发的密钥 ④ 无 DeepFlow 时禁同步器 |

> **边缘 ingest 的角色**：应用 SDK（OTLP exporter）上报到**本集群**的 ingest（就近接收），
> ingest 批量转发到**中心 CH**。这样边缘集群内的应用无需直连中心，网络仅需边缘 ingest 出站可达。

### 3.2 按需组件（3 个）

| 组件 | 启用条件 | 采集内容 | 上报目标 | 说明 |
|---|---|---|---|---|
| **日志 shipper** | 需要 K8s pod 日志 | 容器 stdout/stderr | 中心 VLogs：`http://<中心VLogs>:9428/insert/jsonline` | 轻量 DaemonSet（或复用 event-collector 扩展日志采集）；逐 pod 游标断点续传 |
| **DeepFlow agent** | 需要网络层 eBPF 监控 | 流量/应用性能/网络拓扑 | 中心 DeepFlow server（或边缘独立 deepflow 栈 + 中心同步器拉取） | 边缘 agent → 中心 server 需打通 30033/30035 端口；或边缘全栈部署 |
| **middleware probe**（可选） | 中间件不在本集群 | 由 categraf mysql/redis 插件直连目标中间件 | VM | 若中间件独立部署，categraf 插件 address 指向其地址即可，无需额外组件 |

### 3.3 不需要下发的组件（重要）

| 组件 | 为什么不下发 |
|---|---|
| query-api / frontend / ai-orchestrator | 管理面与智能面全部中心化，边缘仅出数据 |
| ClickHouse / VM / VLogs / MySQL | 数据底座集中中心（隔离网络场景见 §7） |
| 图谱 / 告警引擎 / 审批 / 审计 | 中心统一计算与治理 |
| node-exporter / ipmi-exporter（独立） | 已被 categraf 替代 |
| SNMP 采集 | 已按决策移除（若需交换机监控，categraf snmp 插件按需启用） |

---

## 4. 中心侧需暴露的端点

| 端点 | 协议 | 边缘组件使用 | 建议暴露方式 |
|---|---|---|---|
| ingest:8080 `/v1/traces` `/v1/logs` | HTTP/OTLP | 边缘 ingest 转发 | Ingress/NodePort + X-Api-Key 鉴权 |
| vm:8428 `/api/v1/write` | HTTP remote write | categraf | Ingress（内网/白名单） |
| vlogs:9428 `/insert/jsonline` | HTTP | 日志 shipper | Ingress（内网/白名单） |
| ch:8123 | HTTP | event-collector | **不直接暴露**——经 query-api 内部代理或边缘只写 ingest 转发 |
| deepflow server 30033/30035 | 专有 | DeepFlow agent | 内网/白名单 |

> **安全建议**：以上端点仅对边缘集群网段开放（NetworkPolicy/安全组），并使用独立凭证
> （ingest X-Api-Key、event-collector 用中心下发的 INTERNAL_TOKEN 或独立 token）。

---

## 5. 接入流程（新集群纳管）

1. **中心注册集群**：`POST /api/v1/clusters`（name + kubeconfig）→ 生成 `cluster_id`
2. **下发配置包**：中心生成该集群的 values 覆盖（categraf/event-collector/ingest 的
   `clusterId`、中心端点地址、凭证）——可由 `values-edge-<cluster>.yaml` 模板化
3. **边缘部署**：`helm install aiops-edge ./deploy/helm/aiops-edge -f values-edge-<cluster>.yaml`
   （chart 裁剪为仅含 categraf/event-collector/ingest/日志 shipper 的**边缘子集 chart**）
4. **验证接入**：
   - VM 出现 `cluster_id="<cluster>"` 标签的服务/主机指标
   - CH `k8s_events` 出现该集群事件；`trace_spans`/`log_records` 出现该集群 trace/log
   - 图谱 `build_all(<cluster_id>)` 生成该集群实体与边
   - 告警规则按 `cluster_id` 维度创建，AI 对话带 `cluster_id` 查询

---

## 6. 配置与凭证管理

- **cluster_id 贯穿**：ingest env `CLUSTER_ID`、categraf `global.labels.cluster_id`、
  event-collector env `CLUSTER_ID`、图谱 `(cluster_id, name)` 复合键、告警规则 `Cluster` 字段
- **凭证**：ingest X-Api-Key（每集群可独立密钥）、event-collector 中心 CH 凭证、
  categraf writers 无鉴权（VM 内网白名单）
- **版本同步**：边缘组件镜像 tag 与中心 chart 一致（`global.imageTag`），升级先中心后边缘

---

## 7. 隔离网络场景（联邦式）

若边缘与中心**网络不可直连**（如四网段隔离、DMZ）：
- 每集群独立部署**数据栈**（CH/VM/VLogs/MySQL 全量或轻量）——复用主 chart 完整部署
- 中心通过**管理网**按需拉取（DeepFlow 同步器模式）：中心 ingest 定期从边缘 CH 拉取
  trace/topology 增量（复用 deepflow_sync 的 pull 模式，水位持久化见 B5 修复）
- 图谱/告警/AI 仍在中心，数据经同步入中心 CH 后统一计算

---

## 8. 与现有能力的映射（已实现/待实现）

| 能力 | 现状 |
|---|---|
| ingest 支持 CLUSTER_ID 多集群标签 | ✅ 已实现 |
| event-collector 支持 CLUSTER_ID | ✅ 已实现 |
| categraf cluster_id 全局标签 | ✅ 已实现 |
| 图谱 (cluster_id, name) 复合键隔离 | ✅ 已实现 |
| 告警规则 Cluster 维度 | ✅ 已实现 |
| AICHAT cluster_id 查询 | ✅ 已实现 |
| 多集群模拟数据验证（cluster-b/c） | ✅ 已实现（multicluster_demo.py） |
| **边缘子集 chart（aiops-edge）** | ⏳ 待实现：从主 chart 裁剪为仅采集组件 |
| **日志 shipper 独立组件** | ⏳ 待实现：当前 shipper 内嵌 query-api（中心侧），边缘需独立 |
| **中心端点网络策略模板** | ⏳ 待实现：NetworkPolicy/安全组清单 |

---

> 下一章：《[08 运维与排障补充](./06-ops.md)》（复用）| 相关：01-architecture（中心架构）、
> 04-prod-config（HA/KMS）、SNMP_IPMI_DEPLOYMENT（物理层采集）
