---
title: 故障应急响应流程
tags: [concept, incident, ops]
alert_keys: [IncidentOpened]
applies_to: [all]
---
# 故障应急响应流程

## What this means
标准故障应急流程: 感知 → 收敛 → 定位 → 止损 → 复盘, 确保故障快速恢复并沉淀经验到知识库。

## Immediate checks
1. 确认告警/故障影响面: 涉及服务、用户、时间窗
2. 按严重级别拉起响应: 关键故障优先止损(回滚/限流/切流)而非深挖根因
3. 记录时间线: 故障开始、告警到达、动作与效果

## Likely causes
- 处置顺序建议: 先止损恢复可用, 再分析根因, 最后复盘沉淀
- 联动参考: 对应服务/中间件的诊断 playbook 与历史案例检索

## Escalation criteria
- 30 分钟内未恢复或影响面扩大, 升级指挥与外部协同
- 复盘产出: 案例入库(rag.add_knowledge)并补充/修正 playbook
