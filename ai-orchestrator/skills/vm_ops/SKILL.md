---
name: skill.vm_ops
version: "1.0"
title: 虚拟机运维
description: KubeVirt 虚拟机管理：查询 VM/VMI 列表与状态（含所在节点），执行受限的运维操作（重启/启动/停止/迁移，需人工审批）
when_to_use: 用户询问虚拟机/KubeVirt 状态或需要执行虚拟机运维操作（重启/启动/停止/迁移）时
activation:
  mode: keyword
  keywords: [虚拟机, vm, vmi, kubevirt, 虚机, virtctl, 迁移, migrate, 重启虚拟机, 迁移虚拟机]
tools:
  - name: vm_list
    impl: builtin
    class: read
  - name: vm_status
    impl: builtin
    class: read
  - name: get_vms
    impl: builtin
    class: read
  - name: vm_operate
    impl: builtin
    class: mutating
trigger_actions: [restart, start, stop, migrate]
---
你擅长 KubeVirt 虚拟机运维。查询 VM/VMI 状态并给出管理建议；执行 restart/start/stop/migrate 等
操作会生成待审批任务，需人工确认后才真正执行。
