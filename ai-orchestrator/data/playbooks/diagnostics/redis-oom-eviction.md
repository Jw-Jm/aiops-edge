---
title: Redis 内存淘汰/内存不足
tags: [redis, memory, eviction]
alert_keys: [RedisMemoryHigh, RedisEvictedKeys, RedisOOM]
applies_to: [redis]
---
# Redis 内存淘汰/内存不足

## What this means
Redis 内存达到 maxmemory 触发淘汰策略(如 allkeys-lru)或被 OOM 拒绝写命令, 缓存命中率下降, 大量 key 被驱逐。

## Immediate checks
1. INFO memory 看 used_memory 与 maxmemory; INFO stats 看 evicted_keys 增长
2. 平台查询: Redis 内存使用率曲线与命中率(近 6 小时)趋势
3. 检查淘汰策略: CONFIG GET maxmemory-policy 是否与业务预期一致
4. 平台查询: 各业务 key 空间占用(TopN key), 定位大 key

## Likely causes
- 缓存容量不足或 key 无限增长(未设置过期时间)
- 大 key/热 key 占用量过大
- maxmemory-policy 配置不当(无淘汰策略导致写失败)
- 批量写入/缓存穿透放大内存压力

## Escalation criteria
- 命中率显著下降且写命令报 OOM error
- 需扩容内存或优化缓存策略, 升级容量/变更流程
