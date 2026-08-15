---
title: conntrack 连接跟踪表满
tags: [linux, network, conntrack]
alert_keys: [ConntrackTableFull, NetfilterConntrackDrop]
applies_to: [linux, k8s]
---
# conntrack 连接跟踪表满

## What this means
内核 netfilter 连接跟踪表(nf_conntrack)占满, 新连接被丢弃, 表现为随机性网络超时、SYN 丢弃、服务间偶发连接失败。

## Immediate checks
1. 平台查询: 节点网络指标(连接数/SYN 重传/丢包率)近 30 分钟趋势
2. ssh 节点: sysctl net.netfilter.nf_conntrack_count 与 net.netfilter.nf_conntrack_max 对比
3. dmesg | grep nf_conntrack 看 table full 报错
4. 链路检索: 受影响服务调用失败 span 分布(确认是否集中在特定节点)

## Likely causes
- 高并发短连接场景连接未及时释放(TIME_WAIT 堆积)
- nf_conntrack_max 设置过小(默认 65535 等)
- 外部扫描/异常流量(DDoS)打满连接表
- 服务超时重试风暴放大连接数

## Escalation criteria
- 连接表持续接近 100% 且影响核心业务
- 需调整内核参数或接入 DDoS 防护, 升级为变更处理
