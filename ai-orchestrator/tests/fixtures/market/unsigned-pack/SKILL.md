---
name: skill.market_unsigned
version: "1.0"
title: 无签名市场技能
description: 无签名的市场安装示例技能
when_to_use: 测试未签名 pack 安装时使用
activation:
  mode: keyword
  keywords: [无签名, unsigned]
tools:
  - name: query_metrics
    impl: builtin
    class: read
---
这是一个没有 signature.json 的市场技能，用于验证 unsigned 状态与 REQUIRE_SIGNED 门控。
