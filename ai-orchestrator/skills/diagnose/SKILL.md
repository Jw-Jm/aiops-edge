---
name: skill.diagnose
version: "1.0"
title: 服务诊断
description: 主动探测服务可用性（HTTP/TCP）、查看系统与应用日志，定位服务故障
when_to_use: 用户需要诊断服务连通性、探测端口、查看日志或定位服务故障时
activation:
  mode: keyword
  keywords: [诊断, 探测, 连通, 端口, 日志, 服务状态, probe, journal, tail]
tools:
  - name: probe_http
    impl: builtin
    class: read
  - name: probe_tcp
    impl: builtin
    class: read
  - name: read_journal
    impl: builtin
    class: read
  - name: tail_file
    impl: builtin
    class: read
---
你擅长服务诊断。通过 HTTP/TCP 探测验证服务可用性，通过 journalctl/tail 查看日志定位故障根因。
诊断时先探测可用性，再查日志定位具体错误，直接输出结论。
