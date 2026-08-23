# P20 Plan 2：Helm 安全收紧 acceptance rollout

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Helm 安全收紧（RBAC 撤写权限、移除 orchestrator DB 凭据、渐进启用 default-deny egress）rollout 到 orbstack acceptance，验证功能不受影响且可回滚。

**Architecture:** 基于 `deploy/helm/aiops/` 现有 chart。关键发现：`grantK8sWrite=false`、`injectDbCredentials=false`、`allowOrchestratorDbAccess=false` 已是 fail-closed 默认；唯一未启用的是 `egressDefaultDeny=false`。本计划核心是**渐进启用 egress default-deny**（精确 allow-list + canary + 连通性 precheck + 观察窗口 + 回滚），并复核 RBAC/DB 凭据默认值在生产 values 未被覆盖。

**Tech Stack:** Helm 3、kubectl、orbstack acceptance（namespace `observability`）、NetworkPolicy、RBAC。

## Global Constraints

- GIT_ACTION = NONE：只记录变更，不 commit/push。
- 红线 F1-F5 保持（除 Gate 6 cutover 外，属 Plan 3）。本计划只做安全收紧部署，不触发业务执行变更。
- 渐进 rollout：非一次性收紧；每批启用后验证连通性，异常立即回滚。
- 回滚机制：记录部署前 `helm revision`，异常 `helm rollback <release> <prev_revision>`。
- 只停流量/收紧权限，**不删除任何数据或证据**。
- 不做破坏性数据操作。

---

## Task 1: 复核 RBAC 与 DB 凭据 fail-closed 默认（不 rollout）

**Files:**
- Read: `deploy/helm/aiops/templates/ai-orchestrator/rbac.yaml`
- Read: `deploy/helm/aiops/values.yaml`（`grantK8sWrite`/`injectDbCredentials`/`allowOrchestratorDbAccess`）
- Read: `deploy/helm/aiops/templates/networkpolicy.yaml`

**Interfaces:**
- Produces: 确认 3 个安全开关默认 fail-closed 且生产 values 未覆盖为 true。

- [ ] **Step 1: 核对 RBAC 默认 fail-closed**

确认 `ai-orchestrator/rbac.yaml` 中 `grantK8sWrite`（L30）默认不授予集群写权限；orchestrator ClusterRole 仅保留 pods/replicasets/events/pods/log 只读。记录核对结论。

- [ ] **Step 2: 核对 DB 凭据不注入**

确认 `values.yaml` `injectDbCredentials` 默认 false（L279），orchestrator 无 DB 直连凭据；`allowOrchestratorDbAccess` 默认 false（L31），networkpolicy 不放行 orchestrator→CH/MySQL。记录。

- [ ] **Step 3: 核对生产实际 values 未覆盖**

读取生产使用的 values 覆盖（`helm get values <release>` 或部署 values 文件），确认这三个开关在生产为 false（未被覆盖成 true）。若被覆盖成 true → 记为 P0 缺陷（生产未收紧）。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（包1 RBAC/DB 凭据 fail-closed 复核结论）。

---

## Task 2: egress allow-list 精确设计（P1-1 闭环）

**Files:**
- Modify: `deploy/helm/aiops/templates/networkpolicy.yaml`
- Read: `deploy/helm/aiops/values.yaml`

**Interfaces:**
- Consumes: 各组件 deployment 的 `app` 标签 + 出站需求。
- Produces: 精确 namespace/workload allow-list 定义（DNS/K8s API/query-api/LLM/遥测后端/registry），供 Task 3 canary 使用。

- [ ] **Step 1: 盘点各组件出站需求**

记录每个 workload（query-api/ingest/event-collector/ai-orchestrator/frontend/clickhouse/mysql/victoria-metrics/victoria-logs）的出站目标，重点：

```text
DNS: 所有组件 → kube-dns/coredns (UDP/TCP 53)
K8s API: event-collector/orchestrator/query-api/ingest → kube-apiserver (TCP 6443 或集群实际)
query-api: → orchestrator (8080), → CH, → MySQL, → VM, → VLogs, → LLM(deepseek), → K8s API
orchestrator: → query-api (8080), → LLM(deepseek), → K8s API, → VM/VLogs(若需要), → registry(镜像拉取，init 阶段)
ingest: → VM, → VLogs, → CH
event-collector: → CH, → K8s API
frontend: → query-api（nginx 代理，实际查询由浏览器→NodePort，不需前端出站白名单）
backup/init jobs: → mysql, → clickhouse
```

- [ ] **Step 2: 编写 egress allow-list 模板**

在 `networkpolicy.yaml` 的 `default-deny-egress` 段，为每个需要出站的 workload 补充精确 Egress 规则（to: podSelector/appSelector/ipBlock + ports）。以 orchestrator 为例补全：

```yaml
# allow-orchestrator-egress（合并 DNS/K8s API/LLM/query-api）
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-orchestrator-egress
  namespace: {{ .Values.namespace.observability }}
spec:
  podSelector:
    matchLabels:
      app: ai-orchestrator
  policyTypes: ["Egress"]
  egress:
    - to:  # DNS
        - namespaceSelector: { }
          podSelector: { matchLabels: { k8s-app: kube-dns } }
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
    - to:  # K8s API Server（kube-apiserver，IP 或服务 selector）
        - ipBlock: { cidr: <apiserver-cidr> }
      ports:
        - protocol: TCP
          port: 6443
    - to:  # query-api
        - podSelector: { matchLabels: { app: query-api } }
      ports:
        - protocol: TCP
          port: 8080
    - to:  # LLM（deepseek 域名 → egress 到外部 CIDR/域名解析后 IP）
        - ipBlock: { cidr: <deepseek-cidr> }
      ports:
        - protocol: TCP
          port: 443
```

（其余组件同理补全。apiserver/deepseek 的具体 CIDR 在 Task 3 precheck 时从集群实际解析。）

- [ ] **Step 3: helm lint 校验**

Run: `cd deploy/helm/aiops && helm lint .`
Expected: 0 failed（模板语法正确）。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（allow-list 设计完成 + 组件出站需求表）。

---

## Task 3: egress default-deny 渐进 rollout（canary + precheck + 回滚）

**Files:**
- Modify: `deploy/helm/aiops/values.yaml`（`egressDefaultDeny: false → true`，按 canary 分批）
- Run: `helm upgrade` 到 orbstack

**Interfaces:**
- Consumes: Task 2 allow-list。
- Produces: egress default-deny 启用且功能不受影响；回滚路径验证。

- [ ] **Step 1: 连通性 precheck（启用前）**

启用前用探针验证 allow-list 完整：DNS 解析、K8s API 可达、query-api 可达、LLM（deepseek）可达、遥测后端可达。任一失败 → 修正 allow-list，不启用。

- [ ] **Step 2: 记录 Helm revision**

Run: `helm ls -n observability` 记录当前 release + revision（回滚基准）。

- [ ] **Step 3: canary 启用 egress（先非核心 workload）**

对非关键 workload（如 event-collector/ingest）先启用 egress default-deny（通过 labels/podSelector 分批），观察 DNS/API/遥测连通。

- [ ] **Step 4: 观察窗口**

观察 N 分钟（建议 15-30min）：核心链路（DNS/API/query-api/LLM/遥测）无连通失败、无写失败、日志无 egress 拒绝导致的功能缺失。

- [ ] **Step 5: 推广到全部 workload**

canary 稳定后，将 `egressDefaultDeny: true` 应用到全 namespace，`helm upgrade --install` rollout。

- [ ] **Step 6: 停止条件与回滚**

若出现核心链路连通失败 / 写失败率超阈值 / 功能缺失 → 立即停止并回滚：
Run: `helm rollback aiops <prev_revision> -n observability`
验证：egress 恢复、连通恢复、数据无丢失。

- [ ] **Step 7: 验证功能不受影响**

启用后实测：query-api /health、orchestrator {ok}、frontend Available、ingest/event-collector Running、fresh telemetry 可写、LLM chat 可推理、K8s 只读可查。记录 exit code/响应。

- [ ] **Step 8: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（包1 egress 渐进 rollout 完成 + precheck/观察/回滚结果）。

---

## Task 4: 包1 安全收紧验收

**Files:**
- 无新增（验证）

**Interfaces:**
- Consumes: Task 1-3 全部完成。
- Produces: 包1 验收证据（RBAC fail-closed + DB 凭据不注入 + egress default-deny 启用且功能正常）。

- [ ] **Step 1: 复核集群写权限已撤销**

`kubectl auth can-i --as=system:serviceaccount:observability:ai-orchestrator patch deployment --all-namespaces` → Expected: no（写权限已撤销）。只读（get pods）→ yes。

- [ ] **Step 2: 复核 orchestrator 无 DB 凭据**

检查 orchestrator deployment env，确认无 DB DSN/凭据注入（injectDbCredentials=false）。

- [ ] **Step 3: 复核 egress default-deny 生效**

确认 `default-deny-egress` NetworkPolicy 已应用（`kubectl get netpol -n observability`），且核心链路仍正常（Step 3 的验证结果）。

- [ ] **Step 4: 记录变更（不 commit）**

追加主 Evidence 文档 Phase 20 章节（包1 验收：auth can-i + DB 凭据 + egress 生效）。

**Plan 2 完成标准：** RBAC 集群写权限确认 fail-closed（orchestrator 仅只读）；orchestrator 无 DB 直连凭据；egress default-deny 渐进启用且核心链路（DNS/K8s API/query-api/LLM/遥测）连通无异常；回滚路径验证可执行；`helm lint 0 failed`；生产 values 未把安全开关覆盖为 true。
