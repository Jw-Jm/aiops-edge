# AIOps 平台 — 第二轮覆盖性测试补充报告（R2）

- **测试日期**: 2026-08-18
- **测试方式**: 后端三服务代码审计（ai-orchestrator Python / ai-apm-query-go Go / 采集链路+部署配置）+ 安全实测（认证/越权/注入/暴力破解）+ 浏览器盲区测试
- **范围**: 仅收录**第一轮测试报告与修复方案未覆盖**的新问题。第一轮已覆盖的前端问题、capacity 0.00%、错误率双口径、`_is_info_query` 路由、图谱集群映射等**不重复列出**。
- **标注**: [实测] = 已通过真实请求/浏览器验证；[代码] = 代码审计发现；[潜在] = 当前部署下被代理/配置缓解，但架构上存在风险。

---

## 一、安全（本轮最大盲区，第一轮完全未覆盖）

### 认证与授权

**S1. [代码][潜在] ai-orchestrator 绝大多数 HTTP 端点无认证（BLOCKER）**
- `main.py` 仅全局 `rate_limit_middleware`（:319），**无 auth middleware**；仅少数端点调用 `_require_admin/_require_approver`。`/ai/chat`、`/ai/skills/{key}/execute`、`/ai/agents` CRUD、`/ai/session` DELETE、`/mcp/call`、`/ops/tasks`、`/ops/webhook`、`/ops/rca/*`、`/ops/audit-logs`、`/ai/knowledge*`、`/ai/rules`、`/ai/nl2sql/*`、`/ops/changes*`、`/ipmi/ingest`、`/ai/workflows/{id}/run`、`WS /ops/ws` 等全部开放。
- 设计依赖"query-api 代理做 JWT 认证"（kg_api.py:4 注释明言 trust proxy），但服务绑定 `0.0.0.0:8080`（main.py:2920）且**无任何机制强制请求必须经代理**。
- **实测核验**: 当前部署 orchestrator 端口不可直接访问（连接拒绝），故为**潜在风险**——一旦 pod 被 port-forward/错误 ingress/同网络直连，全部端点（含删除会话、执行 SQL、运行工作流、触发 LLM）即开放。属信任边界脆弱，需纵深防御。

**S2. [代码] Go AuthMiddleware 不校验用户存在/状态（BLOCKER）**
- `auth.go:302-336` 仅校验 JWT 签名+过期，**不查库校验用户是否存在、status 是否=1**；`users.go:224-245` Me 接口在用户不存在时回退返回 token claims。删除/禁用用户后其 JWT（24h 有效）仍可访问全部端点；角色变更也要等 token 过期才生效。无 token 撤销机制。

**S3. [代码][实测] ProxyAI 无角色门控，普通用户可触达高危面（BLOCKER）**
- `settings.go:612-693` ProxyAI 把 `/ai/nl2sql/`、`/ai/shell/check`、`/ipmi/`、`/node/`、`/snmp/`、`/ops/`、`/ai/kg/` 原样转发给 orchestrator（注入 X-Internal-Token），**仅要求有效 JWT，无 admin 门控**。
- **实测确认**: 普通 user 角色可访问 `/ops/audit-logs`(200)、`/ops/tasks`(200)、`/ops/reports/history`(200)、`/ops/changes`(200)；`/users` 为 403。审计日志含 admin 操作与已执行命令，普通用户可读属越权。

**S4. [代码] 基础设施端点无 RBAC（MAJOR）**
- `infrastructure.go:37-97,289-465` Nodes/Pods/Deployments/Namespaces/PodDetail/NodesMetrics/HPA 仅 JWT 保护，任意用户可枚举全集群节点/Pod IP/镜像/资源配额/HPA 状态，无租户/集群隔离。

**S5. [代码] 告警写端点无 admin 校验（MAJOR）**
- `alerts.go:1082` deleteAlertRule、`:954` AlertSilenceByID DELETE、`:826/:858` AlertEventAck/Resolve 均无 admin 检查（创建有），任意用户可删规则/删静默/确认解决告警。

**S6. [代码] 系统端点任意用户可访问（MAJOR）**
- `system.go:13-39` SystemStatus/CacheStats/InvalidateCache/SystemComponents 仅 JWT 保护：任意用户可带任意 pattern 刷新缓存（DoS），SystemComponents 执行 kubectl 探测内部服务泄露集群拓扑。

**S7. [代码] 后端角色词汇与前端脱节（MAJOR，实测确认）**
- 后端创建用户仅接受 `admin|user`（实测 `role must be admin or user`），**不存在 `approver` 角色**；但前端 Approvals/Workflows 检查 `role === 'approver'`。审批人角色在前端永远无法成立，审批权限模型实际失效。

**S8. [代码] 用户无法禁用（MINOR）**
- `users.go:108-111` `if status == 0 { status = 1 }`，更新接口永远无法停用用户。

**S9. [代码] GET /settings/llm 公开（MINOR）**
- `auth.go:314` 公开白名单含 `/settings/llm`，未认证可读 `base_url`（未掩码）+ 部分 API key（settings.go:284-311）。

### 注入与命令执行

**S10. [代码] kubectl exec 白名单允许任意 in-pod 命令（MAJOR）**
- `shell_policy.py:93` `EXEC_WRITE` 含 `kubectl exec \S+ -- `，审批通过后可在**任意 pod 执行任意命令**（`cat /etc/shadow`、`rm -rf /`），元字符检查对 `--` 后的 payload 无效。配合 orchestrator RBAC 集群级 `pods/exec`（rbac.yaml:46-56），一旦 orchestrator 被攻破/提示注入即近似集群管理员。

**S11. [代码] execute_shell 低层函数不强制白名单（MAJOR）**
- `tools.py:145-164` execute_shell 仅查 builtin 规则+黑名单，**不调用** `is_whitelisted_for_execute`/`check_shell_metachars`，`shell=True`。安全完全依赖每个调用方先自查，任何新调用方遗漏即任意 RCE（纵深防御缺口）。

**S12. [代码] SSRF via X-LLM-Base-URL 请求头（MAJOR）**
- `main.py:290-302` 从请求头读 `X-LLM-Base-URL`/`X-LLM-API-Key` 写入 llm_config，orchestrator.py:165 用其构造 LLM client。配合 S1 无认证，攻击者可指向内网地址触发 SSRF + 任意 LLM 调用（成本滥用）。

**S13. [代码] 报告下载路径穿越（MAJOR）**
- `main.py:2051-2075` download_report 用未净化的 `task_id` 拼 `os.path.join(data_dir,"reports",task_id,"report.md")`，`..%2F` 可穿越出 reports 目录；端点无认证，猜 task_id 即可下载含运维数据的报告。`_upload_report`（:1846-1858）同理可越界写。

**S14. [代码] PromQL passthrough 无租户隔离（MAJOR）**
- `victoriametrics.go:79-121,125-156` 任意 PromQL 转发 VM，无租户/集群过滤；QueryRange 无速率限制、`io.ReadAll` 无响应上限。

**S15. [代码] ProxyVictoriaLogs 无租户隔离 + 无界 limit + 任意用户可插入（MAJOR）**
- `handler.go:1950-1982` GET 转发任意 LogsQL，`limit` 无上限；`:1985-2000` POST 允许任意认证用户插入任意日志行，无校验无角色检查。

**S16. [代码] NL2SQL 破坏性请求被静默改写（MINOR，实测确认）**
- 实测"删除所有trace数据"被翻译为 `SELECT * FROM trace_spans ... LIMIT 100` 并执行成功——写保护生效（安全），但**无任何提示告知用户请求被改写为只读查询**，`explanation` 仍写"删除所有trace数据"，语义不符，用户可能误以为删除已执行。

### 凭据与网络

**S17. [代码] 硬编码弱凭据提交到仓库（MAJOR）**
- `deploy/scripts/apply.sh:33-44` 硬编码 `admin123`、`dev-ch-pass`、`dev-mysql-pass`、`dev-internal-token`、`dev-ingest-key`；`values-prod.yaml:48-54` 六个密钥为 `CHANGE_ME` 占位符（`required` 只查非空，`CHANGE_ME` 可通过校验直接部署）。

**S18. [代码] 无 NetworkPolicy + ClickHouse 开放 ::/0（MAJOR）**
- 全 chart 无 NetworkPolicy；`clickhouse/statefulset.yaml:46-48` 默认用户网络开放 `::/0`，MySQL/CH/VM/VL 集群内任意 pod 可达，仅靠口令单层防护。

**S19. [代码] 高危 DaemonSet 权限（MAJOR）**
- event-collector `privileged:true` + 容忍所有污点（deployment.yaml:54-55,19-20）；categraf `privileged + hostPID + /proc,/sys`（daemonset.yaml:31,38-39）；ipmi-exporter privileged（默认关闭）。监控 agent 高爆炸半径。

**S20. [代码] ai-orchestrator RBAC 集群级高危权限（MAJOR）**
- `rbac.yaml:46-56` 授予 `pods/exec` create、`pods` delete、`pods/eviction`、`nodes` patch（cordon/drain）+ 集群级 view。

**S21. [代码] 无登录暴力破解防护（MINOR，实测确认）**
- 实测连续 8 次错误密码后第 9 次正确密码仍 200，无锁定/指数退避；`auth.go:170-182` clientIP 信任 X-Forwarded-For 可伪造绕过限流。

**S22. [代码] JWT 24h 无刷新/撤销（MINOR）**
- 实测 token exp 剩余 23.3h；无 refresh token、无撤销机制（配合 S2 删除用户仍有效）。

**S23. [代码] CORS 全开（MINOR）**
- orchestrator `allow_origins=["*"]`（main.py:183）；Go 端 `Access-Control-Allow-Origin: *`（handler.go:332），无来源白名单。

**S24. [代码] 其余凭据/加固项（MINOR）**
- MySQL 默认 root/空密码（mysql.go:29-35、db.py:6-10）；Grafana TLSInsecure 默认 true（grafana.go:32）；ModelsLLM 无 base_url SSRF 校验（settings.go:556-601）；Grafana 匿名读 + allow_embedding + cookie_samesite none（values-deepflow.yaml:39-44）；ingest API key 为空则不鉴权（ingest main.go:110）；应用容器以 root 运行无 runAsNonRoot/能力裁剪；k8sgpt 把 LLM key 写入子进程 env 可能落盘（orchestrator.py:720-729）。

---

## 二、数据管道正确性（第一轮未覆盖采集链路）

**D1. [代码] K8s 事件 dedup key 缺 name/message（MAJOR）**
- `event-collector/clickhouse.go:34-35` ReplacingMergeTree ORDER BY 不含 name/message，同 ts+object+reason 但内容不同的事件被合并丢失；且 dedup 是最终一致，合并前查询可见重复。

**D2. [代码] DaemonSet 每节点全量 watch 事件 → N 倍重复写（MAJOR）**
- event-collector 为 DaemonSet 且每节点 `K8S_WATCH_ENABLED=true`，N 节点集群同一事件写 N 次，完全依赖（有损 key 的）ReplacingMergeTree 去重，写放大 + 正确性风险。

**D3. [代码] SEL 记录每周期重复插入无 checkpoint（MAJOR）**
- `sel_events.go:96-115` 每 120s `ipmitool sel list last 20` 全量重插，无逐条 checkpoint/去重。

**D4. [代码] ingest WAL compaction 重启后清空未 ack 数据（MAJOR，数据丢失 bug）**
- `wal.go:73-85` 重启只恢复 `seq` 不恢复 `consecutiveAckSeq`（保持 0）；低 seq 已压缩不在文件，Ack 无法推进；文件超 1GB 时 `Compact()` 因 `consecutiveAckSeq==0` 直接 `Truncate(0)` 清空**含未 ack** 全部数据。metrics_writer/log_writer 共用同缺陷。

**D5. [代码] 背压静默丢 span 无计数（MAJOR，不可观测）**
- `writer.go:116-125` 缓冲超 10240 时丢最旧 span 仅打日志；`metrics.go` 有 received/written 但**无 dropped 计数**，数据丢失在 /metrics 不可见。log_writer/metrics_writer 同模式。

**D6. [代码] K8s checkpoint max(ts) 丢乱序事件（MINOR）**
- `k8s_events.go:127-161` 重启以 max(ts) 为断点，晚到/乱序事件被过滤；崩溃时内存缓冲事件丢失（无 WAL）。

**D7. [代码] 无 schema 校验（MINOR）**
- 采集与 ingest 均不校验必填字段/类型，畸形 payload 原样写入或静默跳过。

**D8. [代码] 重试队列溢出丢数据（MINOR）**
- ingest 重试上限 500 批/512MB、event-collector 100 批，超限丢最旧（仅日志）。

---

## 三、可靠性 / 运维（第一轮未覆盖）

**R1. [代码] /health 恒 200 不反映真实健康（MAJOR）**
- event-collector main.go:49-52、ingest main.go:208-211 无条件返回 ok；CH 宕机/重试队列满/丢数据时探针仍通过，liveness/readiness 失效。

**R2. [代码] 后台任务不停止（MINOR）**
- orchestrator `_kg_sched`/`_flow_sched` BackgroundScheduler 优雅关闭不停止（main.py:166-179）；Go alert_engine 循环、data_sync log shipper 永不停止，`logCursors` map 无界增长；HPA/objectEvents goroutine 泄漏。

**R3. [代码] 无超时（MINOR）**
- Go http.Server 无 Read/Write/IdleTimeout（main.go:280），Close() 非 Shutdown()（:295）；k8sClient 无 Timeout（infrastructure.go:26）；TraceContext 每次新建 http.Client + 硬编码 VL URL（handler.go:776-779）。

**R4. [代码] DashboardStats 7+ 查询无缓存（MINOR）**
- handler.go:902-1061 每 60s 轮询跑 ~7 个 CH 查询；cache.go 缓存中间件是死代码从未接线。

**R5. [代码] audit_logs 表从未创建（MINOR，审计链路断裂）**
- audit.go:52 INSERT audit_logs，但全仓无 CREATE TABLE audit_logs，每次审计写入静默失败——审计功能实际不可用（与前端审计日志页展示的数据来源矛盾，需核实是否走其他表）。

**R6. [代码] 内存无界增长（MINOR）**
- orchestrator 限流器 store（main.py:316-333）、Nl2SqlStore（nl2sql.py:100-116）按 IP/查询累积永不清理；无会话 TTL 清理。

**R7. [代码] LLM env 竞态（MINOR）**
- orchestrator.py:157-194 并发调用共享进程 env 设置 OPENAI_*，不同 key 互相覆盖。

**R8. [代码] 其余运维项（MINOR）**
- KG 重建 60s + 每次读端点全表扫描（kg_graph.py:680-785）；报告趋势时区混用（main.py:2023）；报告分页 off-by-one（:1998）；多处错误返回 200 带 error 字段；无 LLM 端点专项限流；无启动配置校验（CH 密码空则无认证连接）；scrape-config query-api job 与 values 注释矛盾；event-collector 无 /metrics；ingest /metrics 缺 dropped 计数；单副本 SPOF（ingest/orchestrator/mysql/clickhouse）；event-collector 内存 256Mi 偏紧。

---

## 四、前端盲区补充（第一轮遗漏）

**F1. [实测] 未知路由无 404 页，静默跳转工作台**
- App.tsx:335 `<Route path="*" element={<Overview />} />`，访问不存在路径直接显示工作台首页，无 404 提示，用户无法感知 URL 错误。

**F2. [实测] 登录失败有错误提示（正常项，记录确认）**
- Login/index.tsx:25 显示 message.error，UX 正常。

**F3. [实测] 用户角色显示与后端脱节**
- 后端 role=user 的用户在用户管理页角色列显示为空/普通成员，与后端 `admin|user` 词汇及前端 `approver` 检查三方不一致（并入 S7）。

---

## 五、结论

第二轮暴露的问题**集中在安全与数据管道**，严重度显著高于第一轮：
- **3 个 BLOCKER**：orchestrator 无认证面（潜在）、Go 认证中间件不校验用户状态、ProxyAI 无角色门控（普通用户可触达审计/执行面）
- **约 20 个 MAJOR**：命令执行白名单过宽、SSRF、路径穿越、凭据硬编码、无 NetworkPolicy、高危 DaemonSet/RBAC、事件去重有损、WAL 数据丢失、背压不可观测、/health 失真等
- 第一轮方案（A/B/C/D/E/F 系列）**完全未覆盖安全与采集链路**，需新增 G（安全）与 H（管道/可靠性）系列。

*本报告仅收录第一轮未覆盖的新问题；与第一轮报告、修复方案 v2 配套使用。*
