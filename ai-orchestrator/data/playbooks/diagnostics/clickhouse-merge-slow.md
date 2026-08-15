---
title: ClickHouse 分区合并慢/积压
tags: [clickhouse, storage, merge]
alert_keys: [ClickHouseMergeBacklog, ClickHousePartsBehind, ClickHouseSlowInsert]
applies_to: [clickhouse]
---
# ClickHouse 分区合并慢/积压

## What this means
ClickHouse 分区(parts)合并积压、合并任务缓慢, 数据分区数膨胀, 查询性能下降, 插入链路可能变慢。

## Immediate checks
1. 平台查询: ClickHouse 存储/查询延迟/parts 数曲线(近 1 小时)
2. SELECT * FROM system.merges 看当前合并任务与耗时; system.parts 看 parts 数与未合并分区
3. 日志检索: clickhouse-server 日志中 merge 相关 warning/error
4. 平台查询: 该实例磁盘 IO 与写入速率指标

## Likely causes
- 高频小批量插入产生大量小 parts
- 磁盘 IO 瓶颈限制合并速度
- 分区键设计不当导致写入/合并热点
- 内存或后台线程数配置不足(background_pool)

## Escalation criteria
- parts 持续膨胀且查询性能明显劣化
- 合并积压超过阈值需调整插入策略或扩容, 升级为变更处理
