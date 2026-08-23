# VictoriaMetrics Scope Label Contract（V9.2 Phase 4 P4.6）

权威 label 契约，供 ingest 写入 VictoriaMetrics（remote write）时统一归一化与校验。

## 必选 label

| label | 类型 | 说明 |
|---|---|---|
| `tenant_id` | canonical UUID | 租户身份，**必选** |
| `cluster_id` | canonical UUID | 集群身份，**必选**，不允许空 / `default` / slug / 数值 |

## 按 scope 决定的 label

| scope | `resource_id` | 说明 |
|---|---|---|
| `resource` | **REQUIRED** | resource-scoped metric（实例级）必须带 canonical UUID resource_id |
| `cluster` | 按契约 | cluster-scoped metric 可不带 |
| `aggregate` | 按契约 | 聚合 metric 可不带 |

## 可选 label

`service`、`namespace`、`instance`、`job`。

## 禁止

- `cluster_id` 用空字符串 / `default` / 任意非 canonical UUID。
- `tenant_id` 为空。

## 说明

VictoriaMetrics remote write 无 DDL；本契约约束写入端 label 归一化，使查询端（query-api）可按 `tenant_id`/`cluster_id`/`resource_id` 精确路由，避免 `default` 桶污染。

## 校验入口

`ai-apm-ingest-go/internal/telemetrylabels.ValidateScopeLabels(labels, scope)`。
