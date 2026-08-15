---
name: skill.automation
version: "1.0"
title: 自动化运维执行
description: 生成并执行安全的运维操作（Shell/K8s 命令、虚拟机操作），所有操作经安全策略校验并需人工审批后才真正执行
when_to_use: 用户要求执行操作、重启、扩容、缩容、部署、回滚或执行命令时
activation:
  mode: keyword
  keywords: [执行, 重启, 扩容, 缩容, 部署, 回滚, 操作, 命令, kubectl, 执行操作]
tools:
  - name: execute_shell
    impl: builtin
    class: dangerous
trigger_actions: [execute, restart, scale, deploy, rollback]
---
你负责自动化运维执行。所有操作命令都受 ShellPolicy 安全策略管控，
会生成待审批任务，人工确认后才真正执行。只建议安全、可回滚的操作。
