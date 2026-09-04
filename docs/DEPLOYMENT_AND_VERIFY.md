# AIOps 平台：部署 + 本机验证指南

本文档说明如何（1）在本机（OrbStack/K8s）部署并验证 AIOps 平台，（2）将同一份代码/Helm 部署到其他环境。命令输出是唯一证据来源；未实际执行的环境项必须标记为 `BLOCKED_BY_ENV`。

> 定位：当前代码为 `RUNTIME_CORRECTNESS_CANDIDATE` / `CONTROLLED_AI_INVESTIGATION_CANDIDATE` / `CONTROLLED_ACTION_CANDIDATE`。
> 生产安全边界（`EXECUTION_MODE=disabled`、orchestrator 无 execute、Stage D 真实执行 keep disabled）按设计保持收敛态，详见《AIOps_全面代码修改报告_V2.md》§28/§29/§35。

---

## 1. 架构概览

| 域 | 组件 | 职责 / 权威 |
|---|---|---|
| Runtime/Trust/Persistence | `ai-apm-query-go` (query-api) | Run/Lease/Commit/Outbox/Recovery/ToolRun/Evidence/Alert 唯一权威；**action 执行唯一入口** |
| Schema | `schema-migrator` | MySQL migration（0001-0016），`aiops_migrator` 账号 DDL |
| Schema/observability | `clickhouse-migrator` | ClickHouse migration（0001-0009），事件身份/quarantine 由部署侧专用 Job 执行；运行时服务只读校验 |
| Semantic reasoning | `ai-orchestrator` | 诊断/调查/动作 propose；**不触发真实 mutation**（无 execute 端点） |
| Ingestion | `ai-apm-ingest-go` | Metrics/Logs/Trace（ClickHouse SoT，span_dedup_key 幂等） |
| Write boundary | `ai-action-executor` | 平台唯一真实 mutation 执行者（Stage D，`disabled` 默认） |
| LLM egress | `ai-llm-egress-proxy` | LLM 出站唯一代理（default-deny，provider key 只存于 proxy） |
| UI | `observability-frontend` | 前端（经 query-api） |

数据流：
```
Metrics/Logs/Trace -> ingest -> ClickHouse / VictoriaLogs / VictoriaMetrics
Run/Lease/Commit/Outbox -> query-api -> MySQL (Runtime SoT)
Agent 诊断 -> query-api /internal/v1/query/*（只读）
动作 propose/approve -> orchestrator -> query-api -> ai-action-executor（disabled）
Trace -> ClickHouse trace_spans（SoT，去重收敛）
```

## 2. 本机（OrbStack）部署

前置：
- OrbStack / Docker Desktop + 本机 K8s
- `kubectl`、`helm`、`docker`
- 本机 `~/.kube/config` 指向目标集群

一键部署（构建全部镜像 + 安装 chart）：
```bash
cd deploy/scripts
ADMIN_PASSWORD=<强随机> CLICKHOUSE_PASSWORD=<强随机> \
MYSQL_ROOT_PASSWORD=<强随机> INTERNAL_TOKEN=<强随机> INGEST_API_KEY=<强随机> \
./apply.sh
```

### 2.0 Fresh Install 验证入口（推荐）

从空的本机命名空间开始时，使用两阶段入口，避免运行时 Deployment 与数据库 hook 互相等待：

```bash
cd /path/to/aiops
export LLM_PROVIDER_KEYS='deepseek:<真实 provider key>'
./deploy/scripts/local-validation.sh --destroy --confirm-destroy
```

该命令按 `generate → build → lint/contract → bootstrap → users-init/schema-migrator → runtime → read-only validator` 顺序执行。`--destroy` 只允许删除固定的 `observability`、`deepflow`、`aiops-canary` 命名空间；没有 `--confirm-destroy` 时脚本拒绝执行。若只检查流程而不改变集群，可运行：

```bash
./deploy/scripts/local-validation.sh --dry-run --skip-deepflow
./deploy/scripts/validate-local-stack.sh --offline
```

本地密钥生成器不会伪造 provider 凭据：必须由调用方显式提供 `LLM_PROVIDER_KEYS`，生成的文件权限为 `0600`。在线校验还会核对 MySQL 迁移 `0001`～`0016`（含 `ai_chat_messages.turn_id` 的跨副本重放约束）、ClickHouse 迁移 `0001`～`0009`、`aiops_app`/`aiops_migrator` 权限、Worker 开关、Proxy `/readyz`、Executor disabled 边界和 canary RBAC；真实指标/日志/事件、真实 provider、DeepFlow、多节点、PITR 和 Credential Broker 若未提供证据，只输出 `BLOCKED_BY_ENV`。

首次部署必须注入 G5 强随机 secret（空值/占位符会渲染失败，fail-closed）。后续升级自动复用已有 `aiops-secrets`。

单独重建某个镜像并更新（发布标签应与 Helm 的 `global.imageTag` 完全一致）：
```bash
IMAGE_TAG=git-<12位源码SHA> ./build-images.sh query-api
kubectl set image deployment/query-api-http query-api-http=query-api:git-<12位源码SHA> -n observability
```

### 2.1 Stage D 接线（executor）密钥
query-api → executor 的 signed context 用**独立** Ed25519 私钥（不复用 RunInvocation issuer key）：
```bash
# 生成私钥（64 字节 Ed25519），公钥给 executor
# 私钥 -> secret AI_ACTION_EXECUTOR_SIGNING_KEY（query-api 签发）
# 公钥 -> executor EXECUTOR_VERIFY_KEYS（仅 realMutation=true 时注入）
```
未配置私钥时，query-api 的 action 执行端点 fail-closed（`EXECUTOR_UNAVAILABLE`），不静默执行。

### 2.2 K8s TLS
生产 `K8S_INSECURE_SKIP_VERIFY` **默认 false**（模板 fail-closed）。query-api 的 log-shipper 显式加载 in-cluster CA（`/var/run/secrets/kubernetes.io/serviceaccount/ca.crt`）验证 API Server 证书。仅本地验证 profile 可显式设 `true`。

### 2.3 发布证据签名绑定（发布审计/审批背书，非 Helm 部署技术前置）

`collect-release-evidence.sh` 不把本地 tag、Docker content digest 或环境变量当作发布身份。
采用受控签名发布审批时（见 §4.2），生产流水线额外提供一个由受控签名密钥签名的
`release-binding.json`，并设置：

```bash
export AIOPS_RELEASE_RENDERED_MANIFEST=/path/to/rendered-manifest.yaml
export AIOPS_RELEASE_BINDING_FILE=/path/to/release-binding.json
export AIOPS_RELEASE_SIGNATURE_FILE=/path/to/release-binding.json.sig
export AIOPS_RELEASE_SIGNATURE_PUBLIC_KEY=/path/to/release-signing-public.pem
```

绑定文件的 `schema_version` 必须为 `1`，并包含与当前候选完全一致的 `git_commit`、`image_tag`、
`rendered_manifest_sha256`、每个服务的 `images[].immutable_digest`，以及非空的
`migration_digests`、`policy_digests`、`data_digests`（每项均为 SHA-256）。采集器会先对绑定
原始字节执行 Ed25519 验签，再将绑定内容与当前 HEAD、Docker/registry 证据和 rendered manifest
摘要逐项比较；任意错配、缺项或重放都会保持 `publishable=false`。该判定用于正式版本审计、
发布审批与供应链背书，**不是 Helm 生产部署的技术前置条件**（见 §4.2）。

## 3. 本机全量验证清单

部署后逐一验证（本仓库所有验证均在真实本机环境跑通）：

### 3.1 服务健康
```bash
kubectl get deploy -n observability   # 全部 1/1
kubectl get pod -n observability | grep -v Running | grep -v Terminating
kubectl logs -n observability -l app=query-api-http --tail=20   # 无 executor 配置错误 / FATAL
```

### 3.2 单元/集成测试（本机真实依赖）
```bash
# query-go（Runtime/Control Plane 全部能力 + Stage D 接线单测）
cd ai-apm-query-go && go test ./...

# orchestrator（1132 tests）
cd ai-orchestrator && .venv314/bin/python -m pytest tests/ -q

# ingest（Trace sink + 幂等）
cd ai-apm-ingest-go && go test ./...

# LLM proxy（强认证 + key-isolation）
cd ai-llm-egress-proxy && go test ./...

# frontend
cd observability-frontend && npx tsc --noEmit
```

### 3.3 真实依赖 E2E（需要真实 MySQL/ClickHouse/K8s）

**Stage D 执行闭环**（真实 MySQL + 真实 executor `disabled`）：
```bash
# port-forward MySQL + executor
kubectl port-forward -n observability svc/mysql 13306:3306 &
kubectl port-forward -n observability svc/ai-action-executor 18082:8080 &
cd ai-apm-query-go
E2E_AIACTION=1 \
MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 MYSQL_USER=root \
MYSQL_PASSWORD=$(kubectl get secret aiops-secrets -n observability -o jsonpath='{.data.MYSQL_ROOT_PASSWORD}' | base64 -d) \
MYSQL_DB=aiops \
AI_ACTION_EXECUTOR_URL=http://127.0.0.1:18082 \
AI_ACTION_EXECUTOR_SIGNING_KEY=<私钥> \
go test ./internal/api/ -run TestE2E_ActionExecution -v
# 预期：PASS（query-api 签名被真实 executor 验签；disabled → rejected；落库 rejected/EXECUTOR_REJECTED）
```

**Trace 去重/故障/租户 E2E**（真实 ClickHouse）：
```bash
kubectl port-forward -n observability svc/clickhouse 18123:8123 &
cd ai-apm-ingest-go
TEST_CH_URL=http://127.0.0.1:18123 TEST_CH_USER=default \
TEST_CH_PASSWORD=$(kubectl get secret aiops-secrets -n observability -o jsonpath='{.data.CLICKHOUSE_PASSWORD}' | base64 -d) \
go test -tags integration ./internal/tracesink/ -run TestCHSpanSinkReal -v
# 预期：dedup / failure-recovery / tenant-isolation 全 PASS
```

**Alert DB-time 验证**：
```bash
# alert-eval leader 接管（DB-time 原子抢占，epoch 递增）
mysql> SELECT holder_id, holder_epoch, expires_at FROM aiops.alert_eval_leader;
# rule state upsert（updated_at 由 DB CURRENT_TIMESTAMP 生成）
mysql> SELECT rule_id, breach_streak, updated_at FROM aiops.alert_rule_runtime_state;
```

**MySQL 备份恢复演练**（临时实例，不碰生产）：
```bash
docker run -d --name pitr -e MYSQL_ROOT_PASSWORD=x -p 13307:3306 mysql:8
# 从生产 mysqldump 导出 aiops -> 导入临时实例 -> 验证表/数据完整
```

### 3.4 安全边界验证
- `EXECUTION_MODE=disabled`：executor 拒绝任何真实 mutation（403 `real mutation not permitted`）。
- orchestrator 无 execute 端点：`ops_action_api.py` 只有 propose/list/get/confirm。
- `K8S_INSECURE_SKIP_VERIFY=false`：log-shipper 用 in-cluster CA 连接 API Server。
- NetworkPolicy：default-deny ingress + 组件白名单；executor 仅 query-api 可访问；LLM 出站仅 proxy。

## 4. 部署到其他环境

同一份代码 + Helm chart 可直接部署到任意 K8s（生产/预发/云）。差异点：

1. **镜像仓库**：`IMAGE_REGISTRY=<registry>/aiops IMAGE_TAG=<tag> ./build-images.sh all` 构建并推送。
2. **secret 注入**：首次部署必须注入强随机 secret（同上 §2）；升级复用。
3. **K8s TLS**：生产保持 `K8S_INSECURE_SKIP_VERIFY=false` + CA/kubeconfig。
4. **NetworkPolicy**：`networkPolicy.enabled=true`（default-deny）；LLM/executor 按启用开关渲染。
5. **多节点/HA**：`BLOCKED_BY_ENV`（报告 P1）——需真实多节点验证 worker failover、跨节点 PVC、network partition、multi-AZ。单节点本机已验证；生产多节点需额外验证。

### 4.1 其他环境特有关注
- **MySQL**：生产用云 RDS/StatefulSet，需 backup/PITR/failover 演练（本机已验证备份可恢复 + binlog ROW format 就绪）。
- **ClickHouse**：Trace SoT 用 ReplacingMergeTree 去重；跨多副本写入已验证幂等。
- **LLM 集成**：需要真实 provider key 才能启用 proxy 转发；未配置时 proxy fail-closed（无 key 无转发），且 NetworkPolicy 不渲染 LLM 规则。
- **Stage D 真实执行**：`EXECUTION_MODE` 必须保持 `disabled`，直到 Credential Broker 真实接通 + post-verify/audit 闭环（`BLOCKED_BY_ENV`）。切 `approved` 前务必先完成 Credential Broker + rollback/post-verify/audit。

### 4.2 publishable / 签名绑定门禁：不阻塞离线包制作与目标环境部署（确认清单）

**结论**：`publishable` 判定与 Ed25519 签名绑定（§2.3 / `collect-release-evidence.sh` / `verify-release-signature.sh` / `verify-release-binding.sh`）只服务**发布审批层**，不参与离线包制作、镜像搬运或目标环境部署。部署流程**不需要**签发 key、binding 或 release evidence。

已核实的代码事实（2026-09-04，main 0cb0935）：
- `deploy/helm/**`：对 `publishable`/`release-evidence`/`release-binding`/`collect-release-evidence` 引用 **0 命中** —— Helm 渲染与 install/upgrade 不感知该门禁。
- `deploy/scripts/local-validation.sh`（本机部署入口）：对上述符号引用 **0 命中**。
- `deploy/scripts/verify-aiops-workflow-gates.sh`（release-gate 聚合）：对 `publishable` 引用 **0 命中**。

部署人确认清单（满足即不受该门禁影响）：
1. 离线包：构建镜像 → `docker save` → tar 搬运 → 目标环境 `docker load`，**照常进行**。
2. 部署：目标环境 `helm install/upgrade`（含 secret 注入、TLS、NetworkPolicy 等 §4 配置），**不要求任何 evidence/签名**。
3. 全程**不需要**：Ed25519 私钥/公钥、`release-binding.json`、`.sig`、`AIOPS_RELEASE_*` 环境变量。
4. `publishable=true` 只是"发布审批"的可选背书：只有当你们选择"对外/对生产发布前必须由受控签名验证放行"时才需要（§2.3 流程）；不启用则部署完全不受影响。
5. 如需追溯部署镜像版本：用 `docker inspect` 的 `org.opencontainers.image.revision` label 与 registry digest 记录即可，非强制项。

## 5. 准入状态与后续

```
RUNTIME_CORRECTNESS_CANDIDATE
  -> CONTROLLED_AI_INVESTIGATION_CANDIDATE
  -> CONTROLLED_ACTION_CANDIDATE
```
- 本机全量验证通过（A0/B1/B2/C 核心 + Stage D 接线）。
- **生产候选前仍应完成**：Trace/LLM 生产 E2E 扩展、Alert DB-time（已改）、MySQL PITR 完整 binlog 重放演练、多节点 failover（`BLOCKED_BY_ENV`）、Stage D 真实执行前置（Credential Broker 等）。
- 发布禁止条件见《AIOps_全面代码修改报告_V2.md》§35。
