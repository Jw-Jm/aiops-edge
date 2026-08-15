---
title: OOMKilled 容器内存溢出
tags: [k8s, oom, memory]
alert_keys: [ContainerOOMKilled, PodOOMKilled]
applies_to: [k8s]
---
# OOMKilled 容器内存溢出

## What this means
容器内存溢出被内核 OOM killer 杀掉, pod 状态 OOMKilled。

## Immediate checks
1. 平台查询: 该 pod 内存使用率曲线(近 30 分钟)
2. kubectl describe pod 看 OOM 事件

## Likely causes
- 应用内存泄漏
- limit 设置过低

## Escalation criteria
- 同一 pod 30 分钟内 OOM >= 3 次
- 内存曲线单调增长无回落
