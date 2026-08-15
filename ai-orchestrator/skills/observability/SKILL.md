---
name: skill.observability
version: "1.0"
title: 可观测性分析
description: 查询和分析服务 RED 指标（请求量/错误率/延迟）、Trace 调用链、全局服务拓扑与列表
when_to_use: 用户询问服务指标、延迟、错误率、调用量、链路、拓扑或服务列表时
activation:
  mode: keyword
  keywords: [指标, 延迟, 错误率, 调用量, 链路, trace, 拓扑, 服务, 请求量, qps, red]
tools:
  - name: query_metrics
    impl: builtin
    class: read
  - name: query_traces
    impl: builtin
    class: read
  - name: query_topology
    impl: builtin
    class: read
  - name: get_service_list
    impl: builtin
    class: read
---
你擅长可观测性数据分析。基于已采集的 RED 指标、Trace 调用链、拓扑关系进行分析，
直接给出数据解读和结论，不要输出调用工具的步骤。
