---
title: kubelet 异常/停止
tags: [k8s, kubelet, node]
alert_keys: [KubeletDown, KubeletTooManyPods, NodeNotReady]
applies_to: [k8s]
---
# kubelet 异常/停止

## What this means
kubelet 进程异常退出、卡死或证书过期, 导致节点无法上报状态、Pod 生命周期管理失效, 最终节点转为 NotReady。

## Immediate checks
1. ssh 节点: systemctl status kubelet; journalctl -u kubelet --since "1 hour ago" | grep -i error
2. 检查 kubelet 证书: kubectl get csr 看 Pending/过期; 检查 /var/lib/kubelet/pki 证书有效期
3. 平台查询: 节点 CPU/内存/文件句柄指标, 确认是否资源耗尽导致 kubelet OOM
4. 检查容器运行时状态: systemctl status containerd / docker

## Likely causes
- kubelet 证书过期或轮换失败
- 节点资源耗尽(kubelet 被 OOMKilled)
- /var/lib/kubelet 磁盘满或权限损坏
- 运行时(containerd/docker)故障联动

## Escalation criteria
- 节点持续 NotReady 超过 5 分钟
- kubelet 反复崩溃无法自愈, 需重建/重装节点组件
