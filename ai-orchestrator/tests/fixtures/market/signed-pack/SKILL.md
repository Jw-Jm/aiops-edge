---
name: skill.market_demo
version: "1.0"
title: 市场演示技能
description: 市场安装示例技能，引用内置 query_metrics 工具
when_to_use: 测试 marketplace 安装流程时使用
activation:
  mode: keyword
  keywords: [市场, 演示, market]
tools:
  - name: query_metrics
    impl: builtin
    class: read
---
你是一个市场安装的演示技能，用于验证 marketplace 安装/卸载与签名校验流程。
