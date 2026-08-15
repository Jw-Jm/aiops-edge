---
title: Redis 延迟突增
tags: [redis, latency, cache]
alert_keys: [RedisHighLatency, RedisSlowLog, RedisBlockedClients]
applies_to: [redis]
---
# Redis 延迟突增

## What this means
Redis 读写延迟(含 p99)突增, 影响下游缓存命中链路, 表现为接口超时、QPS 下降或错误率上升。

## Immediate checks
1. 平台查询: Redis 实例延迟/QPS/内存/连接数曲线(近 30 分钟)定位突增时间点
2. SLOWLOG GET 100 看慢命令(如 KEYS/SMEMBERS/LRANGE 大 key)
3. 平台查询: 大 key/热 key 扫描结果与命中率
4. 链路检索: 调用链中 Redis 命令耗时, 确认是否为特定业务操作引发

## Likely causes
- 大 key/热 key 导致单命令阻塞
- 慢命令(KEYS * / 大集合操作)或全量持久化阻塞(BGSAVE/FORK)
- 内存碎片或 swap 抖动
- 连接数暴涨排队(连接池打满)

## Escalation criteria
- 延迟持续超过阈值且影响核心业务链路
- 存在大 key 需要拆分或迁移, 升级为变更处理
