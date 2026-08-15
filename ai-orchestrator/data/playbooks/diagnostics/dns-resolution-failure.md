---
title: DNS 解析失败
tags: [k8s, dns, network]
alert_keys: [DNSResolutionFailure, CoreDNSDown, PodDNSConfigError]
applies_to: [k8s]
---
# DNS 解析失败

## What this means
服务/容器内域名解析失败(如 NXDOMAIN / connection refused / i/o timeout), 影响服务间调用与外部依赖访问。

## Immediate checks
1. 平台查询: 目标服务错误率/超时曲线(近 30 分钟), 确认影响面
2. 链路检索: 调用链中 DNS 阶段耗时与失败 span
3. kubectl get pods -n kube-system -l k8s-app=kube-dns 看 CoreDNS pod 状态与重启
4. 日志检索: CoreDNS 日志(拒绝/超时查询)与业务容器 /etc/resolv.conf 内容

## Likely causes
- CoreDNS 副本数不足或资源受限(Pod 被 OOM/驱逐)
- node-local-dns/coredns 网络策略或 service 被阻塞
- 上游 DNS(集群外部/etcd 后端)不可达
- pod 使用 hostNetwork 或自定义 dnsConfig 指向错误 DNS

## Escalation criteria
- CoreDNS 不可用导致大范围解析失败
- 超时 15 分钟未恢复, 升级网络/DNS 专项处理
