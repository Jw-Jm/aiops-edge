---
title: OOMKilled 容器内存溢出
tags: [k8s, oom, memory]
alert_keys: [ContainerOOMKilled, PodOOMKilled, KubePodCrashLooping]
applies_to: [k8s]
---
# OOMKilled 容器内存溢出

## What this means
容器内存超过 limit 被内核 OOM killer 杀掉, pod 状态 OOMKilled / CrashLoopBackOff。

## Immediate checks
1. 平台查询: 该 pod 内存使用率曲线(近 30 分钟)与 limit 对比
2. kubectl get pod <pod> -n <ns> -o yaml 看 lastState.terminated.reason
3. kubectl describe pod 看 OOM 事件与 exit code 137

## Likely causes
- 应用内存泄漏
- limit 设置过低
- 流量突增瞬时峰值

## Escalation criteria
- 同一 pod 30 分钟内 OOM ≥ 3 次
- 内存曲线单调增长无回落(疑似泄漏, 建议 dump heap)
