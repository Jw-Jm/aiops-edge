---
title: 节点磁盘压力 DiskPressure
tags: [k8s, disk, storage]
alert_keys: [NodeDiskPressure, NodeFilesystemSpaceFillingUp, PodEviction]
applies_to: [k8s]
---
# 节点磁盘压力 DiskPressure

## What this means
节点磁盘(根分区或 kubelet 数据目录)使用率超过阈值, 节点进入 DiskPressure 状态, 触发 Pod 驱逐与镜像/日志回收。

## Immediate checks
1. 平台查询: 该节点磁盘使用率曲线(近 6 小时)与各挂载点占用分布
2. kubectl get node <node> -o yaml 看 node.kubernetes.io/disk-pressure 污点与 condition
3. kubectl describe node <node> 看 Allocatable 与污点; ssh 节点 df -h 定位高占用挂载点
4. 日志检索: containerd/kubelet 日志中 Evicted / image GC 相关记录

## Likely causes
- 日志/镜像堆积未及时回收(容器 runtime 垃圾回收配置过松)
- 业务数据写满数据盘(PVC 或本地目录)
- 大量已删除但仍被占用句柄的文件(inode/空间无法释放)

## Escalation criteria
- 磁盘使用率持续 >85% 且增长无回落
- 已发生 Pod 驱逐(Evicted)或新建 Pod 调度失败
- 需扩容节点或迁移数据盘, 升级为变更工单处理
