# VictoriaLogs Scope Label Contract（V9.2 Phase 4 P4.6）

权威 label 契约，供 ingest / shipper / collector 写入 VictoriaLogs 时统一归一化与校验。

## 必选 label

| label | 类型 | 说明 |
|---|---|---|
| `tenant_id` | canonical UUID | 租户身份，**必选**，不允许空 |
| `cluster_id` | canonical UUID | 集群身份，**必选**，不允许空 / `default` / slug / 数值 |

## 按 scope 决定的 label

| scope | `resource_id` | 说明 |
|---|---|---|
| `resource` | **REQUIRED** | resource-scoped 日志（如某服务某实例）必须带 canonical UUID resource_id |
| `cluster` | 按契约 | cluster-scoped 日志可不带 resource_id |
| `aggregate` | 按契约 | 聚合/汇总日志可不带 resource_id |

> Phase 4 不强制所有日志都必须有 `resource_id`；仅 resource-scoped 日志强制。

## 可选 label

`service_name`、`namespace`、`pod`、`severity`、`trace_id`、`span_id`。

## 禁止

- `cluster_id` 用空字符串 / `default` / 任意非 canonical UUID（如 `orbstack`、`prod-cluster`、`123`）。
- `tenant_id` 为空。
- 用 slug/name 代替 canonical UUID。

## streamFields 建议

VictoriaLogs `streamFields` 建议至少包含：`tenant_id`、`cluster_id`、`service_name`（多租户多集群隔离必须含 tenant_id + cluster_id）。

## 校验入口

`ai-apm-ingest-go/internal/telemetrylabels.ValidateScopeLabels(labels, scope)`；归一化用 `NormalizeScopeLabels`。
