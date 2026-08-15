---
title: 节点 NotReady
tags: [k8s, node, kubelet]
alert_keys: [NodeNotReady, KubeletDown, NodeHeartbeatLoss]
applies_to: [k8s]
---
# 节点 NotReady

## What this means
节点与 apiserver 心跳丢失或 kubelet 异常, 节点状态转为 NotReady, 其上 Pod 无法正常调度与服务(无容灾时)。

## Immediate checks
1. kubectl get node <node> 看状态与 age; kubectl describe node 看 conditions 最后更新时间
2. 日志检索: kubelet 日志(node 侧)与 apiserver 侧 node 心跳相关记录
3. 平台查询: 节点 CPU/内存/磁盘/网络指标曲线(近 1 小时)判断是否资源或网络故障
4. ssh 节点: systemctl status kubelet; journalctl -u kubelet 最近报错

## Likely causes
- kubelet 服务停止或 OOM
- CNI 网络插件故障导致 Pod 网络异常
- 节点磁盘满(含 /var/lib/kubelet)或 etcd 压力大导致心跳超时
- 节点负载过高(kubelet 无法及时上报心跳)

## Escalation criteria
- 节点 NotReady 超过 5 分钟或影响有状态服务
- 多节点同时 NotReady(疑似网络分区或 etcd 故障)
- 物理/底层资源故障需机房介入
