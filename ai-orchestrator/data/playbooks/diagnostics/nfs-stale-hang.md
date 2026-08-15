---
title: NFS 挂载卡死/Stale
tags: [nfs, storage, hang]
alert_keys: [NFSStaleHandle, NFSMountHang, PVIOError]
applies_to: [nfs, k8s]
---
# NFS 挂载卡死/Stale

## What this means
NFS 挂载失效(服务端重启/IP 变更)导致 stale file handle 或 IO hang, 应用读写挂死, 容器可能无法重启(K8s 挂载阻塞)。

## Immediate checks
1. 平台查询: 受影响服务错误率/IO 等待曲线(近 30 分钟)
2. 链路检索/日志检索: 应用日志中 stale file handle / nfs: server not responding
3. ssh 应用节点: mount | grep nfs; df -h; dmesg | grep -i nfs 看服务端连接状态
4. 检查 NFS 服务端状态与网络连通: showmount -e <server> / ping / 端口 2049

## Likely causes
- NFS 服务端重启/IP 变更/导出版本不一致
- 网络抖动或防火墙拦截 NFS 端口
- 长时间无 IO 会话超时(soft vs hard 挂载参数)
- PVC/PV 绑定错误或服务端路径被移除

## Escalation criteria
- 核心业务 IO 长时间挂死或 Pod 无法重启
- 需重启 NFS 服务端或调整挂载参数, 升级为变更处理
