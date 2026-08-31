# AIOps 平台

本仓库是 AIOps 平台的当前实现入口。生产架构以 `docs/architecture/index.md`、
`SECURITY.md` 和部署 Chart 为准；历史方案仅用于迁移背景，不得作为运行时契约。

## 服务边界

```mermaid
flowchart LR
  UI[Frontend\nHttpOnly Cookie] --> Q[query-api-http\nHTTP/授权/查询]
  Q --> R[query-run-dispatch\nRun/Action outbox]
  Q --> E[query-alert-eval\n告警评估]
  R --> W[investigation-worker\n签名 Run 执行]
  W --> Q
  Q --> I[unified ingest\n唯一遥测写入口]
  C[event-collector\nK8s/SEL adapter] --> I
  Q --> P[LLM egress proxy]
  Q --> B[credential broker]
  B --> K[Kubernetes TokenRequest]
  Q --> M[(MySQL\nIAM/Run/Chat/Action SoT)]
  I --> CH[(ClickHouse\nobservability data)]
```

## 本地验证

只读合同、Helm 和测试命令见 `docs/runtime-slo.md` 及 `deploy/scripts/`。生产密钥、
证书、图谱 gate manifest 和候选环境证据必须在发布流水线注入，禁止提交到 Git。

## 关键入口

- 架构与数据所有权：`docs/architecture/index.md`、`docs/ownership/data-owners.md`
- 安全边界：`SECURITY.md`
- 运行时预算：`docs/runtime-slo.md`
- 部署：`deploy/helm/aiops/`
- 发布证据：`deploy/scripts/collect-release-evidence.sh`
