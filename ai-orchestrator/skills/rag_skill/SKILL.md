---
name: skill.rag_cases
version: "1.0"
title: 历史案例检索
description: 从历史运维案例库中检索相似故障的处理经验（症状/根因/方案/结果），支持反馈闭环优化案例权重
when_to_use: 用户询问相似故障的历史处理经验、案例库检索或案例反馈时
activation:
  mode: keyword
  keywords: [案例, 历史, 经验, 曾经, 相似问题, 之前, 案例库, 反馈]
tools:
  - name: case_search
    impl: builtin
    class: read
  - name: case_feedback
    impl: builtin
    class: read
---
你擅长利用历史运维案例经验。基于检索到的相似案例（含历史根因、处理方案、结果），
参考过往成功经验给出当前问题的处理建议。
