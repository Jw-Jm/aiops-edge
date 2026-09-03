# 生产整改剩余阻断项完成计划

## 目标

在不降低冻结设计门槛、不使用 fixture 冒充生产证据的前提下，完成当前报告中仍可由代码、配置和本机环境修复的生产整改；对必须依赖正式 registry、KMS、硬件或多节点环境的项目形成明确的 fail-closed 验证门禁和发布材料。

## 约束

- 所有代码变更先新增可复现失败测试，再实现，再执行定向和全量验证。
- 不连接生产、不使用生产凭据、不执行生产迁移；OrbStack 仅用于隔离验证。
- 不降低 RCA `confirmed` 阈值，不把 `probable`、`partial` 或 fixture 结果转换为 confirmed。
- 任何缺失外部证据的项目保持 `未验证` 或 `BLOCKED_BY_ENV`，并在报告中列出补证要求。

## 执行任务

### 1. RCA 证据闭环与硬件事件映射

- 检查 `ai-orchestrator/rca_engine/{engine,runtime,scorer}.py`、`apps/investigation.py`、`ai-event-collector` 的 evidence envelope 和评分输入。
- 为硬件/变更/共故障证据缺字段、错误类型和时间窗新增 RED 测试；实现仅基于真实字段的规范化，不合成证据。
- 验证 RCA 结果始终满足 `root_score == deterministic_root_score`、最终图上下文、bounded path 和独立类别门槛；真实环境证据不足时继续返回 `probable`/`partial`。
- 运行 RCA/AICHAT/事件采集定向测试和本机只读真实链路；记录可复核 run id、ToolRun 和 evidence 摘要。

### 2. ChatTool 审计与失败回放门禁

- 检查 `ai-apm-query-go/internal/api/internal_query.go`、`chat_tool_wrapper.go`、`ai_chat_tool_runs.go` 以及迁移 `0017_ai_chat_tool_runs.sql`。
- 补充并发同 key、参数冲突、Start 早于数据源 I/O、Finish 失败 fail-closed、running/terminal 重放测试。
- 在本机 MySQL/Query Pod 验证审计行和数据源调用次数；不把审计摘要当作观测正文。

### 3. 发布物完整性与镜像绑定

- 检查 `deploy/scripts/collect-release-evidence.sh`、Helm values/templates 和镜像构建脚本。
- 增加源码 HEAD、镜像 digest、渲染 manifest、迁移/policy/data 摘要的一致性校验；签名材料缺失时明确失败。
- 重建自研镜像并部署同一 commit；验证 Pod imageID、Helm revision、迁移 Job 和回滚脚本。正式 registry/KMS/SBOM 仍只记录为外部门禁，不能用本地 tag 代替。

### 4. 凭据、证书与服务身份

- 检查 Helm Secret 引用、mTLS SAN guard、TrustedRequestContext、防重放 nonce 和 Credential Broker 开关。
- 增加错误 Secret、错误 SAN、过期/轮换材料和重放请求的本机隔离测试；保持 mutation 默认 disabled。
- 输出候选环境所需的 per-service SAN、ExternalSecret/KMS、轮换和撤销验收命令，生产材料缺失时保持阻断。

### 5. Graph 容量、恢复与 HA 门禁

- 检查 Graph schema/source/reconcile、alias projection、分页和自环过滤；确认 200k vertices/1M edges 门禁不执行压测。
- 验证冷启动、增量/断点恢复、租户隔离、查询超限和 Graph 不可用时的 partial/stale 语义。
- 生成绑定当前 commit/digest 的本机容量与恢复证据；跨节点 p95、PITR、RPO/RTO、正式存储和 KMS 证据列为未验证发布门禁。

### 6. 报告与发布门禁更新

- 用实际命令、提交、Helm revision、镜像 digest、测试输出更新 `AIOps平台生产整改实施与复审报告.md`。
- 逐项标注通过、失败或未验证；列出最小发布阻断集合和可执行验收标准。
- 完成前执行工作区、测试、合同、Helm、证据 validator 自检，不提交临时凭据或运行时数据。

## 完成判定

只有当所有可本机修复项有代码与测试证据、所有外部依赖项有明确阻断和补证要求、报告与 HEAD/运行态一致时，才可结束本计划；生产发布结论仍由真实 marker、正式 registry/KMS、HA/PITR/RPO/RTO 和安全材料门禁共同决定。
