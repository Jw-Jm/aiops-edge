# AIOps 全链路智能运维平台 · 整体设计文档 v1.1

- **版本**: v1.1（v1.0 经组件精简 + 开源协议合规审核修订）
- **日期**: 2026-08-16
- **修订要点**: ① 采集组件以 categraf 为中心精简（4 合 1）② 数据库组件 6→4（移除 Redis 死代码消费 + MinIO AGPL 风险）③ 新增开源协议合规审计结论 ④ 组件清单全量对照
- **依据**: 三轮代码勘探 + 全功能实测 + 生产化差距审计 + categraf 能力调研（官方文档/GitHub v0.5.17）+ 15 项组件许可证逐一核实 + 参考《AIOps 根因定位》方法论

---

## 0. 设计原则（自我审核后的核心立场）

1. **证据链第一性原理**：AI 根因候选必须带证据链接（指标/日志/链路/变更/事件/案例），可复核、可驳回。
2. **三层贯通优先于单层做强**：物理层→平台层→应用层的故障传播链通过知识图谱统一关联。
3. **平台层优先级 ≥ 物理层**：中间件/K8s/网络/存储是日常故障主体，数据基础大半已存在，先做平台层贯通。
4. **数据基础先于 AI 放大**：链路 ID、变更时间线、事件落库、工单结构化未补齐前不堆 LLM 复杂度。
5. **图谱自动构建为主、人工修正兜底**；防"自动构建串数据"是红线。
6. **多集群隔离是红线**：实体以 `(cluster_id, name)` 复合键区分。
7. **生产化是智能化的地基**：P0 加固先行。
8. **（v1.1 新增）组件极简 + 协议洁净**：采集端一个 agent 覆盖多职责（categraf）；数据库只保留必要组件；全栈依赖开源协议可闭源商用。

---

## 1. 目标与范围

**一句话定位**：从服务器硬件（IPMI/SNMP）→ 平台基础设施（K8s/中间件/网络）→ 应用（服务/链路/日志）自动采集、统一关联（知识图谱）、智能诊断（证据链 RCA）与安全处置（审批闭环）的**生产级、组件极简、协议合规的全链路 AIOps 平台**。

**v1.1 相对 v1.0 的核心变化**：

| 维度 | v1.0 | v1.1 |
|---|---|---|
| 独立采集组件 | 6（node-exporter/ipmi-exporter/ingest/DeepFlow/+新增 middleware-exporter/k8s-event-collector） | **4**（categraf/ingest/DeepFlow/event-collector） |
| 数据库服务 | 6（CH/VM/VLogs/MySQL/Redis/MinIO） | **4**（CH/VM/VLogs/MySQL） |
| 存储引擎 | 8（+ChromaDB 嵌入式/SQLite） | **5**（+ChromaDB 嵌入式） |
| 协议风险组件 | MinIO（AGPL+停更）、Redis（8.x 协议复杂化） | **零硬性风险**（见第 10 章） |

---

## 2. 现状基线（关键事实索引）

- 后端：query-api(Go)/orchestrator(FastAPI)/ingest(Go)/ipmi-exporter 四自研 + 8 中间件；DeepFlow 独立 ns
- 数据底座：CH 9 表（trace_spans/log_records/service_topology/alert_events 等，30d TTL）、VM（RED+node 指标）、VLogs（K8s pod 日志 15K 条 shipper）、MySQL 30 表（业务+图谱属性图）、Redis（**Go 侧 cache.go 注释明言"伪 Redis 死代码已移除"；Python 侧唯一消费者 tasks.py 为死代码**）、MinIO（仅 `_upload_report` 报告留档）、ChromaDB 嵌入式（RAG 77 案例）、SQLite（会话 checkpoint）
- 实测确认问题：AI 取数不一致、多轮断裂、RBAC 建规则缺口、LLM test 接口 400、webhook 任务卡 diagnosing、mysql 探针 bug、无 PDB/备份、DeepFlow agent 熔断、vmalert 空壳、告警事件内存态上限 1000、LLM 并发=4
- 平台自身 LICENSE：**Apache 2.0**（README 声明"全部自研"）

---

## 3. 总体架构

### 3.1 逻辑架构（四横一纵，v1.1）

```
┌────────────────────── 控制面（安全治理）──────────────────────┐
│  RBAC(admin/user+scope) │ 审批门控 │ 审计(全组件) │ 密钥(KMS) │ 配额 │
└─────────────────────────────────────────────────────────────┘
┌────────────────────────── 智能运维层（AI）──────────────────┐
│ AICHAT(SSE) │ RCA证据链引擎 │ 告警引擎(并行化) │ SLO │ 容量ETT │
│ NL2SQL │ 自动调查闭环 │ 处置建议+审批+执行 │ 复盘回写 │ 工作流  │
└──────────────────────────────┬──────────────────────────────┘
                    证据查询（图路径展开）
┌────────────────────────── 知识图谱层（三层统一证据层）★ ─────┐
│ 实体: sensor/server/switch → node/pod/middleware/cluster → service │
│ 关系: RUNS_ON│DEPENDS_ON│CONNECTS_TO│HAS_CHANGE│RAISES│CAUSED_BY │
│ 自动构建管线(5min增量+对账) │ 查询API │ 存储(MySQL→Kuzu演进)   │
└──────┬───────────────┬───────────────┬───────────────┬───────┘
       │ 物理层         │ 平台层         │ 应用层         │ 事件层
┌──────▼──────┐ ┌───────▼────────┐ ┌──────▼──────────┐ ┌─▼────────────┐
│ categraf:   │ │ categraf:      │ │ ingest(自研):   │ │ event-       │
│ IPMI/SEL    │ │ 中间件深度指标  │ │ OTLP trace/log  │ │ collector:   │
│ SNMP 轮询   │ │ (MySQL/Redis/  │ │ → CH+VM         │ │ K8s 事件+    │
│ 主机指标    │ │ CH)            │ │ 日志shipper     │ │ SEL 明细     │
└─────────────┘ └───────────────┘ └──────────────────┘ └──────────────┘
                              │
                    ┌─────────▼─────────┐
                    │ 数据底座: CH / VM  │
                    │ VLogs / MySQL(4)  │
                    │ ChromaDB(嵌入式)  │
                    └───────────────────┘
```

### 3.2 部署架构（集中式多集群）

```
┌──────── 中央管理面（生产集群，HA） ────────┐
│ frontend×2 │ query-api×2 │ orchestrator×2 │
│ 图谱/告警/AI/审批/审计                     │
│ CH(Replicated×2) VM VLogs MySQL(PXC)      │
└───────────────┬──────────────────────────┘
        采集汇入（cluster_id 标签 / remote write）
   ┌────────────┼────────────┐
   ▼            ▼            ▼
┌─集群A──────┐ ┌─集群B──────┐ ┌─集群C──────┐
│ categraf   │ │ categraf   │ │ categraf   │
│ (DaemonSet)│ │ (DaemonSet)│ │ (DaemonSet)│
│ event-     │ │ event-     │ │ event-     │
│ collector  │ │ collector  │ │ collector  │
│ ingest转发 │ │ ingest转发 │ │ ingest转发 │
└────────────┘ └────────────┘ └────────────┘
```

---

## 4. 采集层设计（v1.1：categraf 为中心的精简方案）

### 4.1 精简决策依据（调研事实）

| 现有/拟增组件 | categraf 能否替代 | 决策 |
|---|---|---|
| node-exporter | ✅ 完全（内置 node_exporter 核心代码，`node_*` 100% 兼容） | **替代** |
| ipmi-exporter | ✅ 完全（fork prometheus-community/ipmi_exporter；本地 KCS+远程 BMC+SEL+DCMI） | **替代** |
| 自研 SNMP 采集器（orchestrator 内嵌 pysnmp） | ✅ 轮询采集（v1/v2c/v3、符号 MIB、ifTable）；❌ 拓扑发现 | **轮询替代**，LLDP 拓扑发现保留自研 |
| 拟增 middleware-exporter | ✅ MySQL/Redis 完整（连接数/慢查询/复制延迟/命中率）；⚠️ CH 无专门慢查询（用 processes elapsed + text_log 近似） | **不再新增**，由 categraf 承担 |
| K8s 事件采集 | ❌ 无插件 | **保留自研**（并入 event-collector） |
| K8s pod 日志 | ⚠️ 能采但 OSS 链路只能发 Kafka（引入新组件违背精简目标） | **保留现有 query-api 内嵌 shipper**（非独立组件，已实测工作） |
| OTLP trace | ❌ v0.3.55 起移除，官方建议 OTEL Collector | **保留自研 ingest**（平台数据面，不可替代） |
| DeepFlow agent | ❌（eBPF 专用） | **保留** |

**结论：categraf（MIT，单二进制 ~85MB，DaemonSet）一个组件替代 node-exporter + ipmi-exporter + 自研 SNMP 轮询 + 拟增 middleware-exporter 四个职责；K8s 事件与 SEL 明细由新增轻量 event-collector 承担（categraf 明确不支持）。**

### 4.2 categraf 集成设计

- **部署形态**：DaemonSet（每节点主机指标+IPMI 本地 KCS）+ 可选 Deployment（SNMP/中间件集中轮询实例，单副本即可）
- **配置下发**：平台"设备管理/中间件管理"注册 → 平台生成 categraf TOML → `providers=http` 远程下发（categraf 原生支持），设备 CRUD API 保留为配置管理面
- **指标出口**：remote write → VM（现有底座，实测稳定）；全局 labels 注入 `cluster_id/tenant_id`
- **权限**：IPMI 需 freeipmi + 免密 sudo（privileged DaemonSet，与现 ipmi-exporter 同形态）；SNMP 管理网只读
- **已知局限处理**：
  - Metrics writer 无 WAL（内存队列，后端故障丢样本）→ 关键 job（如 IPMI/SNMP）改用其内置 **Prometheus Agent 模式**（WAL 2h）；文档化"短窗口丢失"接受度
  - SEL 仅计数指标（`ipmi_sel_logs_count`）→ **SEL 事件明细由 event-collector 直采 `ipmi-sel` 命令输出落库**（categraf 只做计数告警）
  - CH 慢查询无专门指标 → query-api 直连 CH `system.query_log` 自研查询 + categraf clickhouse 插件近似指标

### 4.3 物理层（v1.1 修订）

| 项 | 设计 |
|---|---|
| IPMI 传感器+计数 | categraf `input.ipmi`（collectors: bmc/ipmi/chassis/sel/dcmi）→ VM |
| **SEL 明细落库** | event-collector 执行 `ipmi-sel list` → orchestrator `/api/v1/ipmi/ingest` 补 sel_events 写入（修复现状表建零写入） |
| SNMP 接口流量 | categraf `input.snmp`（v2c/v3、IF-MIB ifTable）→ VM |
| SNMP 拓扑发现 | 保留自研：orchestrator LLDP 邻居表采集 → 图谱 `CONNECTS_TO` 边（categraf 无此能力） |
| node_health 管道 | 周期任务查 VM（categraf 产出的 node_*/ipmi_* 指标）→ 聚合 `node_component_health` → 告警 |
| 硬件页面 | 前端 `/hardware`：温度热力/电源/风扇/SEL 事件流/SNMP 设备 |

### 4.4 平台层（v1.1 修订）

| 项 | 设计 |
|---|---|
| 中间件深度指标 | categraf mysql/redis/clickhouse 插件 → VM → 告警规则类型 `middleware_metric`（**不再新增 middleware-exporter 组件**） |
| **K8s 事件落库** | 新增轻量 **event-collector**（单二进制 Go）：watch events → CH `k8s_events`（30d TTL）+ **统一承担 IPMI SEL 明细采集**（一组件两职责，替代 v1.0 的 k8s-event-collector） |
| 变更时间线 | `change_events` 表 + `/api/v1/ops/changes`（发布 webhook + 手工登记） |
| 网络告警 | 恢复 vmalert；DeepFlow agent 熔断修复后流量指标→VM |
| 自监控 | query-api 暴露免鉴权 `/metrics` 纳入 VM |

### 4.5 应用层（不变）

OTLP trace/log → ingest → CH + VM（自研数据面保留）；服务口径统一；告警事件持久化 + 引擎并行化。

---

## 5. 数据层设计（v1.1：精简 6→4）

### 5.1 精简决策依据（本地代码事实 + 协议审计）

| 组件 | 现状事实 | v1.1 决策 |
|---|---|---|
| **Redis** | Go 侧 cache.go 注释"伪 Redis 死代码已移除"；Python 侧唯一真实消费者 tasks.py（arq worker）是死代码；其余仅字符串关键词；system.go 仅探活 | **移除**：删 tasks.py 死代码、摘探活；任务状态用现有内存 _task_store + MySQL；顺带消除 Redis 8.x 的 RSAL/SSPL/AGPL 选择题 |
| **MinIO** | 仅 `_upload_report` 报告留档（markdown 文本级）；AGPLv3 + 官方仓库 2026-04 归档、社区版停更不再发二进制 | **移除**：报告存 MySQL `reports` 表 + CH `inspection_reports`（已有）+ 本地 PVC 文件；大产物接入外部 S3 为可选接口（默认不部署任何对象存储组件） |
| **SQLite** | 仅 orchestrator 会话 checkpoint | **并入 MySQL**（新增 conversation_sessions 表） |
| **VLogs** | K8s pod 日志全文检索（LogsQL）；Apache 2.0；双源查询已工作 | **保留**（职责分离合理；合并 CH 需补全文索引且日志量会挤占 CH，属"能力缩水"风险） |
| **CH / VM / MySQL / ChromaDB** | 核心职责不可替代；均 Apache 2.0/GPL(仅 TCP 连接合规)/Apache 2.0 | **保留**（ChromaDB 为嵌入式库非独立服务） |

**结论：独立数据库服务 6→4（CH/VM/VLogs/MySQL），存储引擎 8→5（+嵌入式 ChromaDB）。移除的两个组件恰好是协议风险最大的两个（MinIO AGPL+停更、Redis 8.x 协议复杂化）——精简与合规同向。**

### 5.2 新增表结构

```sql
-- 变更时间线
CREATE TABLE change_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  cluster_id VARCHAR(64) NOT NULL DEFAULT 'default',
  service VARCHAR(255) NOT NULL,
  change_type VARCHAR(32) NOT NULL,          -- deploy/config/scale/network/other
  operator VARCHAR(128) NOT NULL,
  content TEXT NOT NULL,
  related_trace_ids TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_svc_time (cluster_id, service, created_at)
);

-- 会话 checkpoint（替代 SQLite）
CREATE TABLE conversation_sessions (
  session_id VARCHAR(64) PRIMARY KEY,
  thread_id VARCHAR(64) NOT NULL,
  state_json MEDIUMTEXT NOT NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

-- 报告留档（替代 MinIO 主用路径）
CREATE TABLE reports_storage (
  task_id VARCHAR(64) PRIMARY KEY,
  content MEDIUMTEXT NOT NULL,
  service VARCHAR(255), created_at DATETIME
);

-- K8s 事件（CH，30d TTL）
CREATE TABLE k8s_events (
  tenant_id String, cluster_id String DEFAULT 'default',
  ts DateTime64(9), namespace String, kind String, name String,
  reason String, type String, message String, involved_object String,
  source_component String, time_bucket DateTime
) ENGINE = ReplacingMergeTree ORDER BY (tenant_id, cluster_id, ts, involved_object, reason)
  TTL time_bucket + INTERVAL 30 DAY;

-- 图谱实体（扩展现有 topology_nodes/relations，props_json 模式）
-- type ∈ {service, instance, middleware, node, pod, cluster, server, switch, sensor, sel_event, change, alert, case}
-- relation type ∈ {DEPENDS_ON, RUNS_ON, CONNECTS_TO, HAS_CHANGE, RAISES, CAUSED_BY, MENTIONED_IN}
-- 复合键 (cluster_id, type, name) 唯一；props_json 含 created_by(auto|manual)/status
```

### 5.3 备份与恢复（P0）

MySQL mysqldump CronJob + PXC/托管；CH clickhouse-backup + Replicated 副本；VM/VLogs vmbackup→外部 S3（客户侧对象存储，平台不内置）；PVC 快照；季度恢复演练 RPO≤24h。

---

## 6. 知识图谱设计（同 v1.0，保留核心）

本体 Schema（三层实体 + 6 类关系）、6 节点自动构建管线（collect_trace_edges→sync_k8s_entities→attach_middleware→attach_physical→attach_events→reconcile）、查询 API（entity/neighbors/path/impact/evidence/graph）、存储演进（MySQL→Kuzu）、AI 集成（`query_knowledge_graph` 工具 + RCA 证据链展开 + 复盘回写建 `CAUSED_BY` 边）。红线：MySQL 回退仅限全集群、自动构建不覆盖手工修正、查询默认按集群隔离。

**v1.1 增量**：middleware 实体指标来源由 categraf 插件产出（进 VM）→ 图谱属性同步；SEL 事件实体由 event-collector 落库后挂接。

---

## 7. 智能运维层设计（同 v1.0）

告警引擎并行化 + 事件持久化 + 新规则类型（sel_event/middleware_metric/ett_forecast）+ 建规则 admin 门禁；AICHAT 流式/并发队列/QuotaAI 落地/多轮修复/证据链卡片；自动调查闭环（webhook→调查→报告→复盘，diagnosing 超时→failed）；容量 ETT 联动告警。

---

## 8. 前端设计（同 v1.0 + 1 页）

`/hardware`（硬件健康）、`/kg`（图谱视图）、`/changes`（变更时间线）、总览口径统一、告警事件时间窗说明、AICHAT 证据链卡片、多集群切换器。

---

## 9. 生产化设计（同 v1.0，精简后运维面更小）

HA：MySQL PXC/托管、query-api/frontend 多副本+PDB+HPA、CH Replicated、orchestrator 外部化 ChromaDB 后多副本；**Redis/MinIO 移除后 HA 清单缩短两项**；mysql 探针 sh -c 修复；安全：External Secrets、Go 审计中间件、PromQL 透传网段白名单、Ingress+TLS、镜像 digest；自监控：query-api /metrics 免鉴权、采集器心跳告警、组件健康卡接告警。

---

## 10. 开源协议合规（v1.1 新增章节）

### 10.1 结论：精简后方案**满足开源协议**，可闭源商用

- 平台自身代码：**Apache 2.0**（仓库 LICENSE + README 声明）
- **全部保留组件合规**：categraf（**MIT**）、ClickHouse（Apache 2.0）、VictoriaMetrics/VictoriaLogs 社区版（Apache 2.0，**红线：勿用 enterprise 后缀二进制**）、ChromaDB（Apache 2.0 嵌入式）、DeepFlow 核心（Apache 2.0）、Prometheus 全家桶（Apache 2.0）、LangGraph/LangChain/CrewAI/FastAPI（MIT/BSD）、PyMySQL（MIT）
- **MySQL**：仅 TCP 协议连接（PyMySQL MIT 客户端）→ 不触发 GPL；**红线：MySQL server 不与产品捆绑分发**（客户自行部署或托管），捆绑分发才需 Oracle 商业授权
- **已规避的风险组件**：
  - MinIO（AGPLv3 + 2026-04 仓库归档停更）→ **已移除**，报告改 MySQL/PVC，大产物走外部 S3（客户侧）
  - Redis（8.x RSAL/SSPL/AGPL 三选一）→ **已移除**（死代码消费，无损失）
  - DeepFlow 前端（基于 Grafana，AGPL）→ 平台仅 iframe 嵌入使用不分发；对外销售打包场景需替换为自研前端或接受 AGPL 义务

### 10.2 商用场景风险矩阵

| 场景 | 结论 |
|---|---|
| 仅内部部署 | **零硬性风险**（审计后所有组件合规） |
| 对外销售（分发平台自身代码） | 合规；红线：不捆绑 MySQL server/DeepFlow 前端；保留 LICENSE/NOTICE 声明 |
| 对外 SaaS 托管 | 合规；注意不修改任何 AGPL 组件源码（现无 AGPL 组件） |

### 10.3 持续治理

SBOM 扫描（Syft/Grype）进 CI；新增依赖 Allowlist/Denylist 制度（GPL/AGPL 需审批）；分发前交付物许可清单核对；bge 模型权重商用（model card MIT）建议法务最终确认。

---

## 11. 里程碑路线图（v1.1 修订）

| 阶段 | 周期 | 目标 | 关键交付（含精简） |
|---|---|---|---|
| **P0 生产加固** | 1-2 周 | 敢上生产 | mysql 探针修复；备份体系；Ingress/TLS；Go 审计；告警规则 admin 门禁；**移除 Redis/MinIO**（删 tasks.py 死代码、报告改 MySQL）；webhook 调查超时修复；LLM test ULA 豁免 |
| **P1 三层数据贯通** | 1 个月 | 平台层有历史、物理层有数据 | **categraf 替换 node-exporter/ipmi-exporter**；**event-collector**（K8s 事件+SEL 明细）；change_events；SNMP 配置下发集成；LLDP 拓扑自研；node_health 管道；硬件页；口径统一；**SQLite→MySQL** |
| **P2 图谱+AI** | 1-1.5 个月 | 证据链贯通 | 图谱构建管线；kg API；RCA 证据链；AICHAT 取数统一+多轮修复；图谱可视化 |
| **P3 智能闭环** | 持续 | 少人值守 | 自动调查闭环；复盘回写；LLM 队列化；告警并行化；多集群采集器推广（categraf+event-collector 复制） |

**组件精简后运维面收益**：采集组件 6→4、数据库 6→4，Helm Chart 减 4 个部署模板、HA 清单减 2 项、协议风险归零——每阶段工作量相应下降。

---

## 12. 验收指标体系（同 v1.0）

Top3 命中率 / 证据完整率（目标 100%）/ 首轮定位耗时（<5min）/ 误导率（<10%）/ 复盘回写率（>50%）/ 告警误报率 / 数据完整性（SEL/事件/变更 100% 落库）。

---

## 13. 风险与开放问题

1. categraf Metrics writer 无 WAL → 关键 job 用 Prometheus Agent 模式；文档化接受度
2. categraf 日志链路（OSS 仅 Kafka）→ 保留现有 shipper，不引入 Kafka
3. CH/Mongo 慢查询无专门指标 → CH 走 query_log 自研查询；Mongo 不在目标范围
4. DeepFlow 依赖 → 多节点部署缓解熔断；网络拓扑退化用 SNMP LLDP
5. 图谱规模 >50 万实体 → Kuzu 迁移（预留适配层）
6. MySQL 捆绑分发红线 → 部署文档明确"客户自部署/托管"
7. 报告大文件（产物）→ 外部 S3 可选接口，默认本地 PVC

---

## 附录 A：组件清单对照（v1.0 → v1.1）

### 采集组件

| v1.0 | v1.1 | 变化 |
|---|---|---|
| node-exporter | **categraf** | 替代 |
| ipmi-exporter | **categraf** | 替代 |
| 自研 SNMP 采集器（内嵌） | **categraf**（轮询）+ 自研 LLDP（拓扑） | 职责拆分 |
| 拟增 middleware-exporter | **categraf** | 取消新增 |
| 拟增 k8s-event-collector | **event-collector**（K8s 事件 + IPMI SEL 明细） | 扩展职责 |
| ingest（OTLP） | ingest | 保留 |
| DeepFlow agent | DeepFlow agent | 保留 |
| 日志 shipper（内嵌 query-api） | 同左 | 保留（非独立组件） |

### 数据库组件

| v1.0 | v1.1 | 变化 |
|---|---|---|
| ClickHouse | ClickHouse | 保留 |
| VictoriaMetrics | VictoriaMetrics | 保留 |
| VictoriaLogs | VictoriaLogs | 保留 |
| MySQL | MySQL | 保留（+SQLite 并入） |
| Redis | — | **移除**（死代码消费） |
| MinIO | — | **移除**（AGPL+停更；报告改 MySQL/PVC） |
| ChromaDB（嵌入式） | ChromaDB（嵌入式） | 保留 |
| SQLite | — | **并入 MySQL** |

### 许可证速查

| 组件 | 许可证 | 商用 |
|---|---|---|
| 平台自研代码 | Apache 2.0 | ✅ |
| categraf | MIT | ✅ |
| CH/VM/VLogs/ChromaDB/DeepFlow 核心/node-exporter 等 | Apache 2.0 | ✅ |
| LangGraph/LangChain/CrewAI/FastAPI/PyMySQL/redis-py | MIT/BSD | ✅ |
| MySQL server | GPL-2.0 | ✅ 仅 TCP 连接（不捆绑分发） |
| 已移除: MinIO | AGPLv3（停更） | 已消除 |
| 已移除: Redis | RSAL/SSPL/AGPL | 已消除 |

---

## 附录 B：现状事实索引（代码文件）

- 后端路由 `ai-apm-query-go/cmd/api/main.go:69-277`；告警引擎 `alert_engine.go`；缓存 `cache.go`（伪 Redis 已移除注释）；系统探活 `system.go`
- orchestrator：chat `main.py:299`；webhook `main.py:973`；investigator `investigator.py:141-185`；报告 `_upload_report` `main.py:1721`（MinIO 唯一使用点）；死代码 `tasks.py`（arq worker，零引用）；SNMP `snmp_collector.py`
- 物理层：`ipmi-exporter/collect.py`；`ipmi_ingest.py`（sel_events 未写入）；`node_health.py`（mock 端点）
- 图谱底座：MySQL `topology_nodes/relations`（props_json）+ CRUD API `main.go:116-123`
- 多集群：`clusters.go`；CH 全表 cluster_id
- 部署：`deploy/helm/aiops/values.yaml`（nodeExporter/ipmiExporter/redis/minio 段落）；`values-prod.yaml`
- 调研报告：categraf 能力（lib-1 会话）、许可证审计（lib-2 会话）全文见对话历史
