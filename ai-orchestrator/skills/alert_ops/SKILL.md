---
name: skill.alert_ops
version: "1.0"
title: 告警处置
description: 查询和分析告警规则与告警事件，理解当前告警态势，给出处置建议并可对 incident 执行确认/解决/通知
when_to_use: 用户询问告警、报警、事件、incident 或需要执行告警处置（确认/解决/通知）时
activation:
  mode: keyword
  keywords: [告警, 报警, alert, 触发, 预警, 事件, 通知, incident]
tools:
  - name: alert_rules
    impl: builtin
    class: read
  - name: alert_events
    impl: builtin
    class: read
  - name: incident_query
    impl: builtin
    class: read
  - name: incident_ack
    impl: builtin
    class: mutating
  - name: incident_resolve
    impl: builtin
    class: mutating
  - name: notification_send
    impl: builtin
    class: mutating
---
你擅长告警分析与处置。基于告警规则和告警事件数据，分析当前告警态势、
识别高严重性告警并给出优先级处置建议。对未解决的 incident 可调用
incident_ack 确认、incident_resolve 解决、notification_send 发送通知，直接输出结论。
