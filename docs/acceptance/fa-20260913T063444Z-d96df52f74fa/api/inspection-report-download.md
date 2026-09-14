# 巡检报告

- 集群：kubernetes-cluster（91771a6e-9c2d-11f1-8271-bea176fe9f9f / local / local）
- 时间窗：2026-09-12T08:14:13Z → 2026-09-13T08:14:13Z
- 查询合同：inspection-report-v1
- 整体状态：partial
- kubernetes：v1.35.6+orb1
- cluster_slug：local-cluster

## 告警（partial）

来源：observability.alert_events

- 窗口内告警事件：6 条
- 未解决告警：3 条
- 严重度 critical：6 条

## 计算与容量（healthy）

来源：metrics.k8s.io + core/v1 nodes.allocatable

- CPU 使用率：25.36 %
- 内存使用率：64.12 %
- 节点就绪：1/1 节点
- P95 节点 CPU 利用率：25.36 %
- 最大节点 CPU 利用率：25.36 %
- 热点节点数：0 个

## Kubernetes（healthy）

来源：core/v1 pods

- Pod 总数：42 个
- Running：29 个
- Pending：0 个
- 容器就绪：29/29 个
- 容器重启总数：593 次
- 有重启的 Pod：25 个
- 单 Pod 最大重启：115 次

## 网络（not_connected）

来源：未接入

- 缺口：未接入网络吞吐/错误/丢包/时延指标来源；报告中不出现 0 或健康结论

## 存储（not_connected）

来源：未接入

- 缺口：未接入存储使用率/容量/IO 时延指标来源；报告中不出现 0 或健康结论

## 数据质量（partial）

来源：query-api 聚合元数据

- 告警 状态：partial 状态
- 网络 状态：not_connected 状态
- 存储 状态：not_connected 状态

