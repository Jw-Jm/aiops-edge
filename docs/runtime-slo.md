# 运行时预算与发布门禁

## 默认预算

| 调用 | connect | header | overall | retry |
|---|---:|---:|---:|---|
| 内部 HTTP | 1s | 5s | 15s | 仅幂等，最多 2 次 |
| Action execute | 2s | 10s | 60s | 不自动重放，先 reconcile |
| LLM | 3s | 15s | 120s | 仅 429/未开始，最多 2 次 |
| 存储只读 | 2s | — | 30s | 瞬态最多 1 次 |

所有服务必须传播取消、记录统一 trace/request/run/session ID，并以队列高水位背压；禁止
drop-and-ack、无限 timeout、空 catch 后返回成功。

## 发布门禁

本机零失败的单元/静态测试只是必要条件。候选环境还必须提供：迁移/回滚、双副本重启、
依赖中断、Action drift/reconcile、WAL replay、LLM 限流取消、Graph gate、备份恢复和
证书/签名 key 轮换证据。证据由 `deploy/scripts/collect-release-evidence.sh` 生成并绑定版本。

## 可用性与失效行为（P2-HA1：保持 OPEN）

以下只记录客观部署事实与确认状态，不构成任何高可用承诺：

- **业务 SLO：未确认。** 本文件不填写 RTO/RPO，也不以当前部署形态代表"生产高可用"。
- 当前 ai-orchestrator 部署为 **1 副本**，持久卷为 **ReadWriteOnce**；LangGraph checkpoint、session sqlite 与 Chroma/RAG 数据均与 orchestrator 进程本地耦合；canonical Run/Action 状态存 MySQL。
- 客观失效行为：Pod/节点故障时，控制面的可恢复性取决于 Pod 重启与 PVC 挂载恢复；滚动更新期间，新旧实例并存会与 RWO/PVC 单写者约束冲突。
- **处理决定**：保持 OPEN（已知生产剩余风险）。关闭本项需业务正式 SLO 输入后二选一：(a) 实施 checkpoint 外部化、worker lease/fencing、≥2 副本及故障恢复验证；(b) 由业务明确可用性目标与失效行为后收口。在 (a)/(b) 落地前，不得把上述部署事实描述为"满足生产高可用/多副本"。
