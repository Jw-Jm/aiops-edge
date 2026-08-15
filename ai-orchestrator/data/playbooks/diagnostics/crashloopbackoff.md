---
title: Pod CrashLoopBackOff
tags: [k8s, pod, crash]
alert_keys: [KubePodCrashLooping, CrashLoopBackOff]
applies_to: [k8s]
---
# Pod CrashLoopBackOff

## What this means
容器反复启动即崩溃, kubelet 进入指数退避(CrashLoopBackOff)状态, 服务不可用或间歇性可用。

## Immediate checks
1. kubectl get pod <pod> -n <ns> 看 RESTARTS 与状态; kubectl logs <pod> -n <ns> --previous 看上次退出日志
2. kubectl describe pod <pod> -n <ns> 看 Events 与 exit code(137=OOM, 1=启动失败)
3. 平台查询: 该服务错误率/重启次数曲线, 确认是否发布后回归
4. 链路检索: 崩溃前调用链与依赖(DB/Redis/外部 API)是否超时

## Likely causes
- 启动命令/入口脚本错误、配置缺失
- 依赖服务未就绪(DB/Redis 连接不上)
- 资源 limit 过小(启动期即被 OOM)
- 探针(livenessProbe)配置错误导致容器被误杀

## Escalation criteria
- 崩溃导致业务错误率持续上升
- 反复定位无果或疑似代码缺陷, 升级开发与发布变更流程
