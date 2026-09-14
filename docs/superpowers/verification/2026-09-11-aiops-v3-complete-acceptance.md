# AIOps V3 完整改验收说明

> 本说明是 [AIOps V3 实施后完善整改计划](../plans/2026-09-10-aiops-v3-post-implementation-remediation.md) 的正式验收合同，覆盖原 10 项、60 步整改及 2026-09-11 OnGrid 对照审核补充项。开发人员按计划实现，验收人员按本文判定；两者不得用口头解释覆盖失败证据。

## 1. 验收目标与边界

本次验收证明以下结果同时成立：

1. 产品以云平台整体健康为入口，以实际 Kubernetes 集群为资源、观测、调查和处置边界。
2. 容器、Kubernetes Service/Ingress 与 KubeVirt VM/VMI 是主要资源载体；应用、业务、中间件兼容数据不进入默认主界面。
3. 平台只有一个生产产品语义，不出现 prod/dev/staging/test 选择，也不存在“生产平台/生产集群”伪层级。
4. 所有 Kubernetes/KubeVirt 数据严格来自当前授权 cluster_id 对应的真实集群，不能依赖默认 kubeconfig/current context。
5. 健康、图谱、调查和处置均以可追溯事实为依据；缺数据、部分数据和陈旧数据不会被显示为健康或成功。
6. 告警可以按实际集群策略进入人工、草稿或自动只读调查，但任何自动流程都不能直接创建或执行处置动作。
7. 所有页面在规定视口中完整、清晰、无页面级横向溢出，并提供完整内容查看路径。

不在本轮验收范围内：业务应用管理、中间件管理、跨集群批量处置、网络设备独立产品线、ChatOps 主界面、LLM 写操作授权、OnGrid Edge/隧道架构、持久化库存投影的生产实施。

## 2. 判定规则

- 每个检查结果只能是 `PASS`、`FAIL`、`BLOCKED_BY_ENV`。
- 只有外部环境客观缺失且无法安全补齐时可以使用 `BLOCKED_BY_ENV`，并必须记录缺失对象、负责人和解除条件。
- G2 多集群隔离、G4 KubeVirt 边界、G7 调查可信度、G8 处置安全、G11 版本绑定任一 `FAIL` 或 `BLOCKED_BY_ENV`，整体 `publishable=false`。
- loading、empty、error、partial、stale、forbidden 是不同状态；用空态代替错误、用旧数据代替完整数据、用绿色代替未知，一律 `FAIL`。
- 视觉人工签字不能覆盖 API 作用域泄漏、错误健康结论、证据不一致或自动写操作。
- 所有自动化、截图和人工签字必须绑定同一完整 Git commit、镜像 tag 和 Helm revision。

## 3. 验收输入和测试数据

### 3.1 版本输入

验收记录必须包含：

- `git_commit`：40 位完整 commit；
- `image_tag`：`git-<12位源码SHA>`，禁止 `latest`；
- `helm_revision`：本次已部署 revision；
- `rollback_revision`：升级前最后一个 deployed revision；
- `started_at/finished_at`：RFC3339 UTC 时间；
- `operator`：执行者身份，不记录口令、token 或 kubeconfig。

### 3.2 两集群隔离数据集

必须准备两个由当前用户授权的真实 Kubernetes 集群 A/B。两个集群使用相同名称但不同 UID 的对象：

- Namespace：`aiops-acceptance`；
- Pod：`shared-pod`；
- KubeVirt VM/VMI：`shared-vm`，仅在已安装 KubeVirt 的集群创建；
- 每个对象增加 `acceptance-run=<RUN_ID>` 标签；
- 记录两个集群 kube-system UID、对象 UID、resourceVersion。

若无法安全创建测试对象，可使用两个集群中已存在的同名对象，但必须证明 UID 不同。只有一个可用集群时 G2/G4 为 `BLOCKED_BY_ENV`，不能以 mock 或单集群测试替代正式多集群验收。

测试对象清理必须按 canonical cluster_id + namespace + UID 精确执行并保留审计；禁止按名称跨集群批量删除。

### 3.3 状态数据集

自动化 fixture 至少包含：

- 集群：healthy、degraded、critical、unknown/stale；
- API：ready、empty、error、partial、stale、forbidden；
- 图边：structural、runtime、traffic、storage、network、failure-propagation、inferred；
- 调查终止：root_confirmed、evidence_exhausted、budget_exhausted、source_unavailable、cancelled、runtime_failed；
- 告警调查策略：manual、draft、auto_readonly，以及 below_severity、concurrency_limited、rate_limited、invalid_scope；
- 处置：proposed、awaiting approval、approved、executing、verifying、success、partial、failed、regressed、cancelled。

## 4. G0：文档和版本一致性

### 自动检查

Run:

```bash
git diff --check
test "$(git branch --show-current)" = "main"
rg -n "AIOps V3 完整改验收说明" docs/superpowers/verification/2026-09-11-aiops-v3-complete-acceptance.md
rg -n "Task 0:|Task 10:|Task 11:|Task 12:|Task 13:" docs/superpowers/plans/2026-09-10-aiops-v3-post-implementation-remediation.md
```

### 通过条件

- 实施计划、UI 指南和本文互相引用。
- 原 10 项、60 步整改仍可逐项定位，新增任务没有删除原验收条件。
- 代码、镜像和部署证据指向同一 commit。

## 5. G1：静态、单元、集成和构建门禁

Run:

```bash
make test-query-go
make test-frontend
cd ai-apm-query-go && go test ./...
cd ../ai-orchestrator && .venv314/bin/python -m pytest tests/ -q
cd ../observability-frontend && npm test -- --run && npm run build
cd .. && bash deploy/scripts/test-deployment-contracts.sh
bash deploy/scripts/test-production-architecture-contracts.sh
helm lint --strict deploy/helm/aiops
```

通过条件：所有命令 exit 0；不得以历史绿灯抵消本 commit 的失败。测试总数变化时记录真实输出，不在文档中手工维持旧计数。

## 6. G2：真实多集群与 Scope 隔离

### API 检查

1. 登录并选择集群 A，读取资源目录、Pod 详情、VM/VMI 列表和详情、观测、图谱、调查、处置。
2. 所有响应的 `cluster_id`、对象 UID 和关系端点只能属于 A。
3. 切换到集群 B 后重复读取，只能返回 B 的同名对象 UID。
4. 使用集群 A 会话请求 B 的 path/body cluster_id，必须返回 403/404，不能回退 A、默认 context 或名称匹配。
5. 修改凭据使 kube-system UID 与注册身份不一致，请求必须 fail closed；恢复后重新验证。

### 平台页例外

`/overview` 必须聚合当前用户全部授权集群，切换顶栏集群不能改变平台总览聚合结果；平台问题详情跳入集群工作区时才提交并冻结目标集群。

### 通过条件

- A/B 同名对象从未混合，UID 与 resourceVersion 可追溯。
- 日志中没有公共 VM/VMI handler 使用空 kubeconfig/current context 的证据。
- 任一错误集群回退、跨租户数据或名称碰撞均为 `FAIL`。

## 7. G3：平台与集群健康事实

### 平台总览

必须完整显示：平台状态、纳管集群数、健康/降级/严重/未知集群数、采集覆盖分子/分母、最新数据时间、平台组件异常摘要、最高优先级风险。不得显示 0–100 综合分数。

平台状态由最严重的可信集群/平台事实决定；注册 `active/ready` 不能等价于健康。陈旧或未覆盖集群只能是 unknown/stale，不能健康。

### 集群详细总览

选择实际集群后显示该集群的问题队列、八个容器 Kind、Kubernetes Service/Ingress、KubeVirt VM/VMI 和集群基础依赖。控制面、节点/物理宿主、网络、存储和 KubeVirt 能力是依赖状态，不提升为与资源载体并列的产品线。

### 通过条件

- 平台列表、平台聚合、集群详情对同一集群使用相同状态和原因。
- 采集覆盖、最新/最旧有效数据和组件异常均来自真实探测。
- 缺失、partial、stale 时使用明确文案，绝不回退示例数据。

## 8. G4：资源目录和 KubeVirt

### 资源目录

- 默认只展示 Deployment、StatefulSet、DaemonSet、Job、CronJob、Pod、Kubernetes Service、Ingress、VM/VMI。
- Container、ReplicaSet、EndpointSlice 仅进入详情/证据。
- 业务、应用、APM Service、中间件不进入默认目录或一级筛选。
- PVC 是 canonical 存储资源；VM 磁盘只是设备视图，不重复计数。
- NAD 只在 Pod/VMI 真实 Multus 引用中出现。

### KubeVirt

- 未安装 KubeVirt：显示“当前集群未安装 KubeVirt”，不是“0 台虚拟机”。
- 已安装但无 VM/VMI：显示可信空态“当前集群暂无 KubeVirt 虚拟机”。
- API/事件部分失败：仍显示已有 VM/VMI，但标记 partial 和不可用来源。
- VM 详情显示 cluster、namespace、UID、resourceVersion、VMI phase、宿主节点、virt-launcher Pod、接口、卷链和相关 Warning 事件。
- VM 存储链必须为 `VM → VMI → 磁盘设备 → Volume → DataVolume → PVC → PV`；网络链区分默认 Pod 网络与 `接口 → NAD → Multus/CNI → 实际网络`。

### 通过条件

两个集群同名 VM/VMI 严格按 cluster UID 区分；禁止使用默认 kubeconfig；长 UID/镜像/标签可完整查看并复制。

## 9. G5：观测与告警调查策略

### 观测工作区

- 页面按“问题与证据”组织，不按 Prometheus/Loki/Tempo 等数据源产品名组织。
- 每个图表包含指标名、时间范围、单位、来源和图例。
- 错误、空、部分和陈旧互斥；接口错误不能同时显示“当前无问题”。

### 告警调查关联

- `manual`：不自动创建 link/Run；用户显式发起后创建唯一 Run。
- `draft`：新告警创建一条草稿 link，不创建 Run、不消耗 AI 配额；用户点击“开始调查”后创建唯一 Run。
- `auto_readonly`：只有满足最小严重度、并发和每小时上限的告警创建系统 Run；Run 固定 `action_mode=read_only`。
- 同一事件 50 个并发触发、query-api 重启、5 分钟补偿扫描均最多产生一个 link、一个 Run、一个 outbox。
- skipped 必须显示 reason code 的中文含义；不能静默消失。
- 告警恢复只记录 source_resolved，不改写冻结窗口，不取消已开始调查。

### 安全条件

自动调查不得创建 `ai_actions`、审批或 executor 请求。界面始终显示“自动调查仅只读，不会执行处置”。任何自动 Action 证据直接 `FAIL`。

## 10. G6：资源关系图谱

### 数据与预算

- 首屏最多 80 个节点、200 条边；这是渲染预算，不是资源总量或后端查询上限。
- 页面持续显示总匹配量、当前加载量、聚合节点数和等价关系入口。
- 超量通过类型聚合、游标展开和关系列表访问；禁止静默截断。

### 视觉语义

- 每条边直接显示中文关系名称和箭头方向，检查器显示源、关系、目标、事实/推断、权威字段、同步时间和证据。
- 关系语义固定为：结构、运行、流量、存储、网络、故障传播、推断。
- 红色只用于 failure-chain 中 `propagates_failure=true` 的事实边；推断使用虚线并标注；结构边不能因 `propagates_failure` 字段在普通视图变红。
- 节点健康以文字、图标和边框共同表达；健康色不能表示资源类别。
- Namespace/Node 默认折叠为上下文；当其为中心、告警目标、关键传播节点或用户主动展开时显示。VM/VMI 与 Pod/Workload/Service 同级。

### 交互

- 点击节点：保留一跳直接关系和二跳上下游上下文，其他路径弱化。
- 点击边：突出源、目标和该边，右侧检查器同步。
- 点击空白恢复全图；语义筛选只改变显示，不改写原始数据。
- 关系列表提供键盘可达的等价选择；不能强制用户精确点击细线。
- 1024px 时检查器进入 Drawer，画布获得完整宽度。

### 通过条件

验收人任取一条边，无需猜颜色、悬停或阅读代码即可说明“谁以什么关系指向谁、是否事实、是否传播故障、来源何处”。答不出即 `FAIL`。

## 11. G7：调查中心与调查详情

### 调查中心

- 列表首屏只显示资源、症状、状态、证据、发起时间、操作。
- 来源标识为“人工发起”“系统建议”“系统自动”，不显示 synthetic“system”代替未知发起人。
- 完整 cluster ID、Run ID、冻结窗口、根因、置信度、预算和审计进入详情 Drawer/页面，可复制。
- 所有状态中文化；1024/1280 不逐字换行。

### 调查详情首屏

顺序固定为：

1. 结论状态：根因已确认或根因尚未确认；
2. 根因与因果链；
3. 影响资源、实际集群和冻结时间窗；
4. 3–5 条关键事实证据；
5. 反证与缺失证据；
6. 终止原因和预算消耗；
7. 下一步验证或受控动作入口。

完整证据时间线、所有假设、工具活动和原始载荷默认收进“技术详情”。展开后仍必须在视口内滚动，关闭/收起入口保持可见。

### 可信结论

只有同时满足以下条件才显示“根因已确认”：存在根因、confidence ≥ 0.8、至少一条 eligible evidence、非 partial、非 stale。数值置信度必须同时显示支持证据数、反证数、缺失证据数和完整性，不能单独作为真相。

每个终态 Run 必须显示一个终止原因：root_confirmed、evidence_exhausted、budget_exhausted、source_unavailable、cancelled、runtime_failed。`partial` 不能作为原因文本。

因果链每跳都引用当前 Run 的真实图边和 evidence ID；推断边标注“推断”，没有证据的推断不得升级确认结论。

### 通过条件

值班人员在不展开技术详情时，五秒内能回答：当前结论是否成立、影响什么、最关键依据是什么、缺什么、为什么停止、下一步是什么。

## 12. G8：处置安全与展示

- 主列表只显示操作/目标、风险、审批、执行、验证、操作；完整 ID 和审计进入详情。
- 流程固定为 proposal → deterministic preflight → human approval → execute → verify → rollback/audit。
- 审批绑定 action hash/version；执行前重新校验 UID/resourceVersion，变化则拒绝。
- LLM 只能提出建议，不能批准或执行；不增加 OnGrid 式 LLM Reviewer 作为授权者。
- `EXECUTION_MODE=disabled` 时所有真实 mutation 拒绝；切换执行模式必须满足现有安全运行手册。
- 绿色只表示执行成功或验证通过；执行中使用流程蓝；partial/failed/regressed 不得显示绿色。

通过条件：自动调查产生的 Run 关联 Action 数为 0；人工处置可完整追溯预检、审批人、执行、验证、回滚条件和审计。

## 13. G9：智能助手、知识与报告

### 智能助手

- 会话冻结实际集群、可选资源、绝对时间和知识范围；顶栏切换不能静默改写当前会话。
- 回答固定包含结论、事实引用、已发布知识引用、不确定性/缺失证据、下一步、受控动作。
- 没有可引用事实时只能说明缺口，不能生成确定性结论。
- 1024×900 为单主列，会话和上下文进入两个独立 Drawer；输入框不覆盖长回答。

### 运维知识

- 只检索当前租户平台通用知识和当前实际集群知识。
- 草稿不进入 RAG，审核发布后的当前版本才进入索引。
- 类型、范围、来源、版本、审核和索引状态均可见；前端不直接写 Chroma。

### 报告

- 无报告时显示自然高度空态和“前往调查”，不渲染空大表。
- 报告范围绑定实际集群，平台级报告不得被活动集群误过滤。

## 14. G10：完整 UI、响应式与可访问性

### 固定视口

必须生成并人工复核：

- 1440×900：全部主路由；
- 1280×720：平台、集群、资源、观测、图谱、调查、处置、助手；
- 1024×768：平台、集群、资源、观测、图谱、调查、处置；
- 1024×900：助手、知识。

### 通用标准

- 无页面级横向滚动；局部宽表只能在有边界容器中滚动。
- 标题、资源名、状态依据、风险、错误、动作结果都有完整查看路径；Tooltip 不是唯一入口。
- UID、镜像和长标签在自身容器断词并支持复制；正常词语不能逐字断开。
- Drawer/Dialog/Popover 不超出视口，内部滚动，标题和关闭入口可访问。
- 主要交互可用键盘完成；焦点可见；颜色不是唯一语义载体。
- loading/empty/error/partial/stale/forbidden 的高度和动作符合场景，不用大面积空白制造页面感。
- 页面头部最多两个主动作；不提供全局搜索。

### 五秒判读

平台、集群、观测、图谱、调查、处置五类关键页必须让用户在五秒内判断：当前最重要问题、影响范围、依据可信度和下一步主动作。任一页面需要先理解内部表名、状态码或颜色猜测才能操作，判定 `FAIL`。

## 15. G11：真实浏览器、镜像、发布与回滚

### 浏览器门禁

使用真实登录、真实 cluster Scope 和真实 API，检查主路由 HTTP、pageerror、console error、数据状态、固定文案和截图。图谱搜索词来自 `AIOPS_E2E_GRAPH_QUERY`；未配置、无合法资源或图谱不可用均不能记 PASS。

验收 JSON 至少包含：

```json
{
  "schema_version": 2,
  "status": "PASS",
  "git_commit": "40-char-commit",
  "image_tag": "git-12charsha",
  "helm_revision": 1,
  "rollback_revision": 1,
  "gates": {
    "cluster_isolation": "PASS",
    "kubevirt_boundary": "PASS",
    "health_truth": "PASS",
    "graph_semantics": "PASS",
    "alert_investigation": "PASS",
    "investigation_truth": "PASS",
    "action_safety": "PASS",
    "responsive_ui": "PASS"
  },
  "screenshots": [],
  "failures": []
}
```

示例值仅说明结构；生成器必须写入真实 commit/revision 和非空截图清单。`status=PASS` 时 `failures` 必须为空，所有必需 gates 必须 PASS。

### 镜像与部署

- 所有自研镜像使用同一个 tag，OCI `org.opencontainers.image.revision` 等于完整 commit。
- Helm 新 revision Ready 后才运行 UI 验收。
- `collect-release-evidence.sh` 校验验收 JSON、镜像、rendered manifest 和 Helm revision 一致；任一不一致 `publishable=false`。

### 回滚

- 升级前记录 rollback revision。
- 任一阻断 gate 失败，执行 `helm rollback aiops <revision> -n observability --wait --timeout 15m`。
- 保留失败验收 JSON、截图和 release evidence；不能删除失败证据后重新标绿。
- additive 0022 表不要求回滚删除；旧二进制忽略新表，策略缺省 manual。

## 16. P2 库存投影决策门

本项不是本轮发布能力，不进入镜像验收，但 Task 12 必须形成可审计结论。

- 实测 1k/10k/100k 对象和 1/10/50 集群容量基线；未测项写 `NOT_MEASURED`。
- 数据合同包含 cluster identity、full/delta、snapshot generation、resourceVersion、last_seen、删除和 4 MiB 目标/8 MiB 硬分块上限。
- 证明断线恢复、resourceVersion 过期转 full、乱序/重复/漏 chunk 拒绝、跨集群 identity mismatch fail closed。
- 投影新鲜度 p95 ≤ 120 秒，query-api 关键接口 p95 劣化 ≤ 10%，无静默丢对象。
- ADR 只允许采用、暂缓、拒绝一个结论；存在未测或未通过条件时必须暂缓。

## 17. 最终签字

正式验收记录必须由以下角色分别确认：

- 开发：实现 commit、自动化测试和已知失败；
- 运维：真实集群、数据时效、镜像、Helm revision 和回滚点；
- 产品/UI：信息架构、五秒判读、完整显示和固定视口截图；
- 安全/平台负责人：多集群隔离、自动调查只读、处置门禁。

四方确认且 G0–G11 全部 PASS 后，才可把本轮状态记为 `RELEASE_ACCEPTED`。Task 12 的库存投影 ADR 单独记录，不改变本轮发布结论。
