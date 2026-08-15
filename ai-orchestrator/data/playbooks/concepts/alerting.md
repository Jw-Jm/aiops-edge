---
title: 告警体系与告警处理流程
tags: [concept, alerting]
alert_keys: []
applies_to: [all]
---
# 告警体系与告警处理流程

## What this means
介绍平台告警体系的组成(规则/事件/通知)与标准处理流程, 帮助值班人员理解告警含义并快速上手处置。

## Immediate checks
1. 平台查询: 告警事件列表与规则详情, 确认告警来源(阈值/异常检测/日志)
2. 核对告警严重级别(severity)与影响面, 决定响应优先级
3. 查看告警关联的指标/日志/链路上下文

## Likely causes
- 告警分类: 基础设施(k8s/存储/网络)、中间件(DB/缓存/消息)、业务应用
- 告警风暴常见诱因: 规则阈值过松、依赖故障放大、发布变更回归

## Escalation criteria
- 无法判断根因或影响面扩大时升级到对应专项(参考各诊断 playbook)
