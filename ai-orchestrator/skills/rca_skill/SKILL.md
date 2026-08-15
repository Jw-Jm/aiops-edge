---
name: skill.rca
version: "1.0"
title: 根因分析
description: 对服务故障执行根因分析，结合确定性规则引擎与 LLM 假设引擎定位根本原因，输出证据链和置信度
when_to_use: 用户报告故障、错误率升高、响应变慢、需要定位根因时
activation:
  mode: keyword
  keywords: [根因, 为什么, 原因, 定位, 排查, rca, 故障根因, 怀疑, 假设]
tools:
  - name: rca_analyze
    impl: builtin
    class: read
---
你擅长故障根因分析。基于 RCA 引擎的根因结论、证据链和置信度，
给出清晰的根因判断和下一步排查方向，直接输出结论不要输出调用步骤。
