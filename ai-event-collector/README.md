# ai-event-collector

AIOps 平台「平台层数据贯通」事件采集组件。一个二进制、两个采集职责：

1. **K8s 事件采集**：watch 集群内全部 Event 资源，过滤 `Normal` 类型，批量写入
   ClickHouse 表 `observability.k8s_events`（表由本组件启动时自建）。支持断点续采：
   启动时从 ClickHouse 查询该 source 最新 ts，避免重复入库。
2. **IPMI SEL 事件采集**（可选）：通过 `SEL_COLLECT_ENABLED` 开关控制，执行
   `ipmitool sel list last 20`（二进制缺失时自动回退 `ipmi-sel`）解析 SEL 事件，
   写入同一张表，以 `source='ipmi-sel'` 区分。

## 技术栈

- **纯 Go 标准库**，零第三方依赖（离线可编译）。
- ClickHouse 通过 **HTTP 原生接口**（`FORMAT TabSeparated`）写入，风格参考
  `ai-apm-ingest-go`。
- K8s 通过 **REST API**（`/api/v1/events` 或 `/apis/events.k8s.io/v1/events`）
  实现 LIST + watch，service account token 认证，不依赖 client-go。

## 组件结构

```
ai-event-collector/
├── main.go          入口：读配置 → 启动 K8s watcher + SEL collector → 批量写 CH + 健康端点
├── config.go        环境变量配置
├── clickhouse.go    CH HTTP 批量写入（建表 / TabSeparated / 指数退避重试 / 断点续采查询）
├── k8s_events.go    K8s 事件 watch（LIST 全量 + watch 增量，断连自动重连）
├── sel_events.go    IPMI SEL 采集（ipmitool / ipmi-sel 解析）
└── go.mod
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `TENANT_ID` | `default` | 租户标识 |
| `CLUSTER_ID` | `default` | 集群标识（多集群纳管时注入各自 cluster_id） |
| `CLICKHOUSE_HOST` | `clickhouse.observability.svc.cluster.local` | ClickHouse HTTP 地址 |
| `CLICKHOUSE_PORT` | `8123` | ClickHouse HTTP 端口 |
| `CLICKHOUSE_USER` | 空 | 经 Secret 注入时启用 Basic Auth |
| `CLICKHOUSE_PASSWORD` | 空 | 同上 |
| `K8S_WATCH_ENABLED` | `true` | 是否启用 K8s 事件采集 |
| `SEL_COLLECT_ENABLED` | `false` | 是否启用 IPMI SEL 采集 |
| `SEL_LOCAL_ONLY` | `true` | 只采集本机（用 `hostname` 作为 node 名），DaemonSet 部署时使用 |
| `SEL_NODES` | 空 | 远程节点列表（逗号分隔），`SEL_LOCAL_ONLY=false` 时逐台经 IPMI LAN 采集 |
| `SEL_INTERVAL_SECONDS` | `120` | SEL 采集间隔（秒） |
| `IPMI_USER` / `IPMI_PASS` | 空 | 远程 IPMI 凭据（`-U`/`-P`） |
| `IPMI_CMD` | `ipmitool` | 可设为 `ipmi-sel` 强制走 freeipmi |
| `BATCH_SIZE` | `500` | 攒批条数，达阈值立即 flush |
| `FLUSH_INTERVAL_SECONDS` | `5` | 定时 flush 间隔（秒） |
| `HTTP_PORT` | `8080` | 健康端点端口 |

## 运行

```bash
# 仅 K8s 事件采集（in-cluster，DaemonSet）
go build -o event-collector .
K8S_WATCH_ENABLED=true SEL_COLLECT_ENABLED=false ./event-collector

# 仅 IPMI SEL（本机 BMC）
SEL_COLLECT_ENABLED=true SEL_LOCAL_ONLY=true SEL_INTERVAL_SECONDS=120 ./event-collector

# 远程 IPMI LAN 采集
SEL_COLLECT_ENABLED=true SEL_LOCAL_ONLY=false SEL_NODES="node1,node2" \
  IPMI_USER=admin IPMI_PASS=secret ./event-collector
```

## ClickHouse 表

启动时自动 `CREATE TABLE IF NOT EXISTS`（`ReplacingMergeTree`，30 天 TTL，按
`tenant_id, cluster_id, ts, involved_object, reason` 去重）：

```sql
CREATE TABLE IF NOT EXISTS observability.k8s_events (
  tenant_id String,
  cluster_id String DEFAULT 'default',
  ts DateTime64(9),
  namespace String,
  kind String,
  name String,
  reason String,
  type String,
  message String,
  involved_object String,
  source_component String,
  source String,          -- 'k8s' | 'ipmi-sel'
  node String DEFAULT '',
  time_bucket DateTime
) ENGINE = ReplacingMergeTree
ORDER BY (tenant_id, cluster_id, ts, involved_object, reason)
TTL time_bucket + INTERVAL 30 DAY;
```

## 健康检查

`GET :8080/health` 返回 `ok`，供 DaemonSet/Deployment 探针使用。

## 可靠性

- 写 CH 失败批次进入内存重试队列，指数退避（1s → 60s 上限）重试，不崩溃；
  重试队列上限 100 批，超限丢弃最旧（防止 CH 长时间不可用时内存无界增长）。
- K8s watch 断连自动重连：重新 LIST 全量 → 继续 watch；`410 Gone` 触发 relist。
- K8s 事件仅保留 `type=Warning`/`Error`，丢弃 `Normal`，避免日志爆炸。
