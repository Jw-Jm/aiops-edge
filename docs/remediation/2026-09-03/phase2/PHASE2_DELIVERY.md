# Phase 2 交付汇总：提高门禁真实性（2026-09-03）

> 整改分支 `remediation/p1-release-blockers-20260903`。依据审核报告 §24 Phase 2 顺序。

| 项 | 核心动作 | 验证 |
|---|---|---|
| **P2-F1** | workflow contract 改 **真 MySQL Go integration**（`ai-apm-query-go/internal/api/workflow_contract_mysql_test.go`）：驱动生产 handler/DAO/executor-client 覆盖报告 §18.2 **14 场景**（create/approve/outbox claim/dispatch/executor success/lost response→unknown/reconcile/dup approval/dup dispatch/stale version/idem-key change/rejected/scope mismatch/hash tamper）；fake HTTP executor（报告允许）；删除 `tests/workflow-e2e` Python 自建 Harness + 源码字符串伪测试；CI `workflow-contract-tests` 改 Go job + mysql:8.4 service | 真 MySQL **9/9 PASS**；本机全量 go test PASS |
| **P2-F2** | ① gateway tool registry 运行时测试（执行注册+断言 capability/backend/selectable/execution disabled），替换源码字符串检查；② **修复 RBAC toggle 假开关**（`grantK8sWrite` values 存在但模板从未引用——grant 脚本无效）→ ClusterRole 条件 verb + Helm render 运行时测试（prod 只读/grant 受限 patch/revoke 收敛） | 5 + 4 passed |
| **P2-S6B** | egress proxy token 比较改 `subtle.ConstantTimeCompare`（空 token fail-closed + 长度差 false）；补正确/错误/空测试 | go test PASS |
| **P2-R5** | `main.py` LLM_MOCK 默认 `"true"→"false"`（显式 opt-in）；llm_mock docstring 收敛；补 main 层默认 false/local+true 允许/prod+true FATAL 测试 | 13 passed |
| **P2-A2** | 前端 interceptor 读内存 `scopeRuntime`（uiStore hydrate/switch 同步；模块加载仅一次恢复），删除每请求 `JSON.parse(localStorage)`；cluster_id 语义注释明确为过滤参数非授权边界 | tsc exit=0 |
| **P2-SEC1** | Helm 全局容器 hardening（drop ALL caps + `allowPrivilegeEscalation=false` + seccomp RuntimeDefault）接入全部自研 workload；credential-broker/event-collector 保留 `readOnlyRootFilesystem=true`+seccomp；ai-orchestrator Dockerfile 非 root（USER 65532, chown /app /var/lib/aiops） | gate helm+contracts exit=0；非 root 镜像 `id=65532` + `import main` OK |

## P2-F1/P2-F2 发现并修复的真实缺陷

1. **RBAC toggle 是"假开关"**：`grant-orchestrator-ops.sh` 的 `--set grantK8sWrite=true` 在模板中从未被引用——grant 实际不授予任何写权限（revoke 同）。已接线为受控条件 verb（deployments patch，默认 false）+ grant/revoke/prod 语义测试。
2. **Python e2e 三个文件两个是源码字符串伪门禁**（读 Go 源文本断言关键词），一个自建 Harness 状态机（测试自己的 dataclass）——无法发现任何真实 store/dispatcher/reconcile 回归。已整体移除并由 Go 真 MySQL 测试替代。
3. 执行中发现 0005 迁移硬编码 `aiops.` 库前缀（隔离测试库无法全量迁移）——测试以"跳过 0005 顺序建表"绕行，**未改动生产迁移文本**（避免生产 checksum 漂移）。

## 诚实边界

- **P2-SEC1 剩余专项**（后续治理，非 Phase 2 阻断）：query-api/ingest/executor/egress/frontend 等其余组件 Dockerfile USER 非 root；orchestrator `readOnlyRootFilesystem=true`（需 WAL/数据写路径 emptyDir/PVC 化）；nginx 非特权。记录于本文件。
- **P2-A2 完整显式 scope**（"更推荐"级）：`GLOBAL_PATHS` 前缀列表保留为过滤语义（新路由需维护），未改为逐 API 声明 scope——调用方破坏面过大，需独立前端重构任务。验收 #1（interceptor 无 JSON.parse）已达成。
- P2-F1 Go integration 由 CI mysql:8.4 service 支持；本机真 MySQL（orbstack mysql-0）验证 9/9。

## 关闭结论

Phase 2 六项均完成代码侧落地并本地验证。真实 CI（含新 workflow-contract job）等待最新 run `33761040877` 结果。
