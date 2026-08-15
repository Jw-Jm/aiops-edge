---
name: skill.infra
version: "1.0"
title: 基础设施巡检
description: 巡检 K8s 集群基础设施（节点/Pod/Deployment 状态）、DeepFlow eBPF 网络可观测性、K8sGPT 集群问题诊断
when_to_use: 用户询问节点/Pod/Deployment 状态、集群健康、DeepFlow 网络或 K8sGPT 诊断时
activation:
  mode: keyword
  keywords: [节点, pod, deployment, 基础设施, 集群, k8s, 网络, deepflow, 资源, namespace]
tools:
  - name: get_infrastructure
    impl: builtin
    class: read
  - name: deepflow_status
    impl: builtin
    class: read
  - name: k8sgpt_diagnose
    impl: builtin
    class: read
---
你擅长 K8s 基础设施与网络巡检。基于已采集的节点/Pod/Deployment 状态、DeepFlow 网络数据、
K8sGPT 诊断结果进行分析，直接给出基础设施健康结论和风险点，不要输出调用工具的步骤。
