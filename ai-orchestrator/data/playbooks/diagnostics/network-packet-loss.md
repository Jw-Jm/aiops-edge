---
title: 网络丢包/重传
tags: [network, packet-loss, deepflow]
alert_keys: [NetworkPacketLoss, NetworkRetransmission, HighNetworkLatency]
applies_to: [network]
---
# 网络丢包/重传

## What this means
服务间网络出现丢包、TCP 重传或延迟抖动, 表现为接口偶发超时、重试增多、错误率小幅上升。

## Immediate checks
1. 平台查询: deepflow 拓扑中该链路的丢包率/重传率/延迟曲线(近 30 分钟), 定位对端与网段
2. 平台查询: 涉及节点的网络指标(SYN 重传/乱序/丢包)
3. 链路检索: 调用链中客户端-服务端耗时分布, 确认是哪个网段/节点
4. ssh 节点: ping/iperf 验证; ip -s link 看 RX/TX errors; ethtool -S 看 drop/error 计数

## Likely causes
- 网卡/交换机端口故障或速率协商异常
- 链路带宽拥塞(突发流量)
- MTU 不一致导致分片/丢弃
- conntrack/防火墙规则误丢

## Escalation criteria
- 丢包率持续 >1% 且影响核心链路
- 疑似物理链路/交换机故障, 升级网络运维介入
