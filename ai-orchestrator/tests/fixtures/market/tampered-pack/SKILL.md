---
name: skill.market_tampered
version: "1.0"
title: 被篡改签名市场技能
description: 签名被篡改的市场 pack（signature.json 内容非法）
when_to_use: 测试签名校验 failed 状态时使用
activation:
  mode: keyword
  keywords: [篡改, tampered]
tools:
  - name: query_metrics
    impl: builtin
    class: read
---
这是一个 signature.json 签名无效的市场技能，用于验证 failed 状态与安装拒绝。
