# ADR-0001：控制面与数据所有权收敛

- 状态：已接受（当前实现基线）
- 决策：Query API 负责 HTTP、授权、Run/Chat/Action 持久化；dispatcher/evaluator 使用独立进程。
- 原因：避免 HTTP 副本重复调度、跨服务状态漂移和隐式权限。
- 验收：三个独立 binary/Deployment；API 副本扩容不改变 outbox 处理次数。

# ADR-0002：统一身份和作用域

- 状态：已接受
- 决策：MySQL 是 IAM/session/scope 唯一权威；浏览器只使用 HttpOnly Cookie 和服务器返回的 active scope。
- 原因：消除 JWT 角色、`X-Tenant-ID`、默认 tenant/cluster 的越权路径。
- 验收：撤权下一请求生效；缺作用域返回明确 409/403；源码静态合同零 header/default 回退。

# ADR-0003：单一 RCA 与图谱投影

- 状态：已接受
- 决策：RCA V2 是唯一生产引擎，证据必须有实体绑定、provenance group、policy digest；HugeGraph 仅作可重建投影。
- 原因：避免 legacy RCA 与图谱规则产生互相矛盾的结论。
- 验收：每个结论绑定 Run/evidence/policy；Graph gate manifest 与 commit/schema/data 一致。

# ADR-0004：动作凭据和统一采集入口

- 状态：已接受
- 决策：Action Executor 仅接收签名 envelope，经 Credential Broker 获取 ≤300s TokenRequest；Collector 只向 unified ingest 写入，WAL fsync 后返回 receipt。
- 原因：隔离长期 Kubernetes 凭据，消除多套观测数据 owner 和 silent drop。
- 验收：profile 越界、drift、重放、WAL/restart 故障注入均 fail closed 且可恢复。
