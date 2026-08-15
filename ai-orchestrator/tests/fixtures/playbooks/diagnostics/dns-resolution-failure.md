---
title: DNS 解析失败
tags: [k8s, dns, network]
alert_keys: [DNSResolutionFailure, CoreDNSDown]
applies_to: [k8s]
---
# DNS 解析失败

## What this means
域名解析失败导致服务间调用不可达。

## Immediate checks
1. 平台查询: 服务错误率/超时曲线
2. kubectl get pods -n kube-system 看 coredns 状态

## Likely causes
- coredns 副本不足
- 上游 DNS 不可达

## Escalation criteria
- 大范围解析失败
- 超时 15 分钟未恢复
