---
title: Pod Pending 资源不足
tags: [k8s, pod, capacity]
alert_keys: [PodPending, InsufficientResources, NodeUnschedulable]
applies_to: [k8s]
---
# Pod Pending 资源不足

## What this means
Pod 一直处于 Pending, 无法调度到节点, 常见原因为节点 CPU/内存/GPU 等资源不满足 requests 或污点/亲和性不匹配。

## Immediate checks
1. kubectl describe pod <pod> -n <ns> 看 Events 中 FailedScheduling 提示(资源不足/污点/亲和性)
2. kubectl get nodes 看可调度节点; kubectl top node 对比各节点可分配资源与 requests
3. 平台查询: 集群节点资源水位(CPU/内存)近 24 小时, 判断是否整体容量不足
4. 检查 PVC 与 StorageClass 是否就绪(VolumeNodeAffinity 冲突)

## Likely causes
- 集群整体资源不足, 无法满足 requests
- 节点污点(taint)与 pod 容忍度不匹配
- nodeSelector/亲和性/拓扑约束无可用节点
- 存在资源 quota/LimitRange 限制

## Escalation criteria
- 关键业务 Pod 长时间 Pending 无法调度
- 集群容量不足需扩容节点, 升级容量规划流程
