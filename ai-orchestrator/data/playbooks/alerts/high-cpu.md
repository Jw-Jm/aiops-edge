---
title: CPU 使用率过高告警
tags: [alert, cpu, capacity]
alert_keys: [HighCpuUsage, CpuThrottling]
applies_to: [all]
---
# CPU 使用率过高告警

## What this means
主机/容器 CPU 使用率超过阈值触发告警, 可能出现 CPU 争抢、限流(throttle)与延迟上升。

## Immediate checks
1. 平台查询: 告警对象 CPU 使用率曲线(近 1 小时), 确认是持续高还是突发
2. 平台查询: 同主机其他容器/进程 CPU 占用(TopN), 定位占用方
3. 链路检索: 业务 QPS/延迟与 CPU 的关联, 判断是否为流量增长
4. 日志检索: 应用日志中是否有死循环/GC 风暴/定时任务集中执行

## Likely causes
- 业务流量增长导致 CPU 需求上升(需扩容)
- 代码缺陷(死循环/低效算法/GC 压力)
- 定时任务/批处理与在线流量叠加
- CPU limit 过小导致频繁 throttle

## Escalation criteria
- CPU 持续 >90% 且 P99 延迟恶化
- 扩容或限流无法缓解, 升级开发分析代码
