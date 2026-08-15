---
title: 内存使用率过高告警
tags: [alert, memory, capacity]
alert_keys: [HighMemoryUsage, MemoryPressure]
applies_to: [all]
---
# 内存使用率过高告警

## What this means
主机/容器内存使用率超过阈值触发告警, 存在 OOM、内存回收(swap)与性能下降风险。

## Immediate checks
1. 平台查询: 内存使用率曲线(近 1 小时), 判断持续增长还是突发
2. 平台查询: 进程/容器内存占用 TopN, 定位主要占用方
3. 日志检索: 是否出现 OOMKilled / Out of memory 事件
4. 检查 GC 指标(JVM/Python)与缓存/连接池配置是否异常

## Likely causes
- 内存泄漏(长期缓慢增长, 重启后回落)
- 缓存/连接池/线程池配置过大
- 流量突增带来对象膨胀
- limit 设置过低

## Escalation criteria
- 内存持续增长无回落(疑似泄漏)
- 已发生 OOM 且影响业务, 升级开发与容量流程
