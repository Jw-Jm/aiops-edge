---
title: kubectl 常用命令速查
tags: [reference, kubectl, k8s]
alert_keys: []
applies_to: [k8s]
---
# kubectl 常用命令速查

## What this means
常用只读排查命令速查, 供诊断流程中快速定位 pod/节点/事件/资源配额问题。

## Immediate checks
1. kubectl get pods -A -o wide 看全局 pod 状态
2. kubectl get nodes -o wide 看节点状态与角色; kubectl top node 看资源
3. kubectl describe pod <pod> -n <ns> 看事件与容器状态
4. kubectl logs <pod> -n <ns> --previous --tail=200 看崩溃前日志

## Likely causes
- 事件查询: kubectl get events -A --sort-by=.lastTimestamp | tail -50
- 资源配额: kubectl describe quota -A; kubectl get resourcequota -A
- 污点/调度: kubectl describe node <node> 看 Taints 与 Allocatable

## Escalation criteria
- 只读命令无法定位时, 按对应诊断 playbook(oom-killed/node-not-ready 等)继续排查
