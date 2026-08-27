# Trace Summary/Index 查询层设计

## 目标

消除 `GET /api/v1/traces` 直接在 `observability.trace_spans` 上按 `trace_id` 做全量高基数聚合的问题。`trace_spans` 继续作为 Trace 明细的唯一持久化 SoT；Trace 列表改为读取独立的、按 Trace 预聚合的 Summary/Index 层，使在线查询的工作量与摘要行数和时间分区相关，而不再与宽 Span 明细聚合状态直接相关。

## 已验证的故障

当前租户在 `trace_spans` 中约有 4.45M 行，原列表 SQL 需要在明细表上执行 `GROUP BY trace_id`，ClickHouse 在约 3.6 GiB 查询内存上限处返回 `MEMORY_LIMIT_EXCEEDED`，API 转换为 503。ClickHouse 探针从 1 秒放宽到 5 秒是独立的运行稳定性修复；它不属于本设计的容量修复。

## 架构边界

```text
DeepFlow OTLP/gRPC
        |
        v
  ingest -> trace_spans (raw Span SoT)
        |
        +--> trace_spans_to_summary_state (ClickHouse incremental MV)
        +--> trace_spans_to_summary_index (time-ordered candidate MV)
                    |
                    v
          trace_summary_index (candidate Trace IDs)
                    |
                    v
          trace_summary_state FINAL (candidate summaries only)
                    |
                    v
          query-api /api/v1/traces (index -> summary)
                    |
                    +--> trace_spans (detail only, by exact trace_id)
```

不读取或修改 DeepFlow 私有 ClickHouse，不修改 DeepFlow 源码，也不把 Trace 列表降级为抽样或伪造数据。

## 数据模型

`observability.trace_summary_state` 使用 `AggregatingMergeTree`，排序键为 `(tenant_id, cluster_id, date, trace_id)`，每一行是一个租户/集群/日期/Trace 的聚合状态。状态列包括：

- `minState(start_time)` / `maxState(start_time)`：列表时间范围；
- `uniqExactState(span_dedup_key 或 span_id)`：去重后的 Span 数；
- `uniqExactState(service_name)`：服务数；
- `maxState(duration_ns)`：最大耗时；
- `maxState(is_error)`：是否含错误；
- `groupUniqArrayState(service_name/operation_name/http_url)`：服务筛选和关键字筛选所需的摘要索引。

物化视图只对每次进入 `trace_spans` 的数据块计算小范围状态，后台合并负责在相同排序键上合并状态；重复投递不会因计数状态重复累加，因为 Span 数使用稳定去重键，历史空键回退到 `span_id`。`trace_summary_index` 是按日期分区、以 `latest_start_key = -toUnixTimestamp64Nano(latest_start)` 物理排序的轻量候选索引，因此升序读取即为最新优先；五分钟 `time_bucket` 仍保留用于可观测性和回填分块，但不再作为全量列表的排序入口。

## 查询语义

列表 SQL 必须满足以下不变量：

1. 先从 `trace_summary_index` 按日期分区和 `latest_start_key` 取候选 ID，再执行 `FROM observability.trace_summary_state FINAL`；
2. 使用 `finalizeAggregation(...)` 取每行状态，不再出现明细 `GROUP BY trace_id`；
3. 先按精确 `date` 分区和 `latest_start_key` 的物理顺序裁剪，再用 `latest_start` 做精确时间窗口过滤；
4. 服务和关键字过滤仅使用摘要中的数组/Trace ID，不回退到明细表；候选不足时扩大索引候选集，而不是扫描明细；
5. `trace_id` 返回值必须能够继续驱动详情接口，详情查询仍按精确 Trace ID 读取 `trace_spans`；
6. 无 Summary/Index 数据时返回真实空结果或可重试的后端错误，不用明细实时聚合“补数据”。

## 历史回填与增量一致性

部署顺序为：创建 Summary 表 → 以日期分区对既有 `trace_spans` 做一次回填 → 创建/启用物化视图接收后续新数据。回填任务按分区执行，使用独立的后台资源限制，不由用户列表请求触发。回填和物化视图均使用相同聚合表达式；完成后用 Summary 与 `trace_spans` 的抽样/分区计数核对。

回填过程不删除明细、不修改 DeepFlow 数据、不改变原始 TTL。若回填尚未完成，API 只报告摘要层的真实状态；发布验收必须等待目标时间窗口回填完成并核对最新 OTLP 数据持续进入 Summary。

## 故障与降级

- Summary 表/物化视图不可用：Trace 列表返回 `BACKEND_UNAVAILABLE`/`TOOL_TIMEOUT`，不回退到明细全量聚合；
- 明细不可用但 Summary 可用：列表仍可展示摘要，点击详情显示明确的详情错误；
- MV 延迟：列表可以短暂滞后，但不能生成虚假 Trace；通过 ingestion/latest 与 Summary 最新时间监控可观测；
- Summary 状态损坏：通过 schema/migration checksum 和回填验证发现，禁止静默继续发布。

## 验收标准

- 代码测试证明 Trace 列表 SQL 只引用 `trace_summary_index` 与 `trace_summary_state`，不存在明细 `GROUP BY trace_id`；
- Helm/DDL 契约包含表、MV、字段和权限边界；
- 真实环境 Summary 表有历史数据，最新 Summary 时间持续追随最新 Span；
- 真实 API 在连续请求中返回 200、50 条真实摘要，内存错误为 0；
- 任一列表 Trace 的详情 Span 数、Trace ID、服务和错误标识与明细 SoT 一致；
- 1h/6h/24h、服务筛选、关键字筛选、分页均走 Summary 查询；
- DeepFlow Agent 熔断配置仍保持关闭；AI Chat 不在本轮验收范围内。
