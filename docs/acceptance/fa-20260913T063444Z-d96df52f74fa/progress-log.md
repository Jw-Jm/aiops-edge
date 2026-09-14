# 验收执行进度日志（滚动更新）

run_id: fa-20260913T063444Z-d96df52f74fa
基线: docs/AIOps平台最终设计与实施规范.docx (V1.4, 八页 IA)
目标环境: http://localhost:30253/ (kubernetes-cluster 91771a6e + kind-aiops-kind-02 96900c13)

## 已完成

### T0 基线冻结
- 证据目录、环境快照（pods/deployments/helm）、能力覆盖账本（build-capability-ledger.mjs）
- v2 账本：177 条能力全分类（A40/B43/C61/D33），gaps=0（零孤儿能力）

### T1 八页壳层（真实环境验证 ✓）
- 导航顺序：AI 智能运维/总览/集群/全链路监控/知识图谱/知识库/报告/设置
- AI 智能运维为默认首页；无问题/资源/调查/处置等禁止入口
- 旧路由迁移（附录 A）+ 参数透传；navConfig 测试 14/14
- 顶栏 scope 切换真实生效（告警徽标随集群 4→3）；刷新后 scope 自动恢复（preferredClusterId）

### T3-T5 页面与调查链（真实环境验证 ✓）
- AI 智能运维：范围冻结/任务列表 64 个真实 Run/任务阶段/结论等级/候选原因与反证/受控调查入口
- 调查链端到端：创建→outbox 派发→实体解析(UID/名称双路径)→6 类证据(metrics/traces/logs/alerts/changes/events)→确定性评分→诚实结论→证据查询→UI 渲染
- 正样本 run18（deepflow-grafana 真实 Liveness 503）：6 类证据、traces 100 条真实数据、候选根因形成（4 类独立证据、score 0.36、graph_generation 467 固定）、无传播路径边时正确拒绝 Confirmed
- 负样本（无故障实体/未建模对象 regression-svc）：正确拒答，语义区分
  GRAPH_ENTITY_UNRESOLVED（数据事实）≠ GRAPH_UNAVAILABLE（系统故障）
- D15/D16 缺陷闭环（4 层根因：API 字段名/工具注册表/错误分类/证据目标回退）

### T4 全链路监控与知识图谱（真实环境验证 ✓）
- 全链路监控：当前路径/selectedPathId/选择原因/数据质量 + 五面板（端到端依赖/
  指标趋势/性能矩阵/证据表）全部绑定同一 selectedPathId；证据缺口显式标注
- 知识图谱：中心对象/generation 467/返回 2 节点 1 边·渲染 2/1（无数量矛盾）/
  一跳两跳切换/关系事实/影响分析；推荐失败时诚实提示原因

### T6 报告（真实环境验证 ✓）
- 巡检报告：capacity/standard 模板报告在列
- AI 运维报告：新增 POST /ops/reports/ai-operations（从终止 Run 聚合，非终态
  409 拒绝）；报告 #3 生成/列表/下载 200；页面/下载/API 同一 Markdown 投影
- 深链 /reports?type=ai-operations&runId= 自动切桶 + 生成卡片

### 测试
- 前端全量 287/287（74 文件）；RCA 引擎 25 项、D15/D16 回归 4 项通过
- Go：internal/api、internal/graph 全量通过

### T10 部分项
- 安全抽查：settings/llm、kg/ops/sync-states 响应无 secret 泄漏（CLEAN）；
  settings/mcp 403 fail-closed；报告页证据下载走租户/集群隔离校验
- 视觉基准截图 13 张（1600×1000/1440×900/1280×800/1024×768/768×900）
- 768px 窄屏：布局正常、production-width-gate 守卫生效（只读模式提示）

## 已部署镜像版本链
- frontend: v1.4.0-ia8 → v1.4.0-t6
- query-api: v1.4.0-d16b → v1.4.0-t6
- ai-orchestrator/ai-investigation-worker: v1.4.0-d16e
- 关键教训：investigation-worker 容器名为 investigation-worker（非 ai- 前缀），
  且原为 digest 固定镜像，set image 必须用正确容器名

## 未完成（不得标记为完成）
- T11 AI 评测：40 故障场景 × 5 次重复运行、Confirmed 正样本（需图谱传播边）、
  全部第 11 章阈值（Top-1/Top-3/影响范围/重复一致率/p95 时延）
- T10 剩余：WCAG 2.2 AA 全量审计、键盘全操作走查、浏览器兼容矩阵
  （仅 Chromium 实测）、性能压测、提示注入/MCP 工具风险/危险动作/故障注入/
  恢复回滚的专项测试
- T2 十态全覆盖的逐页状态验证（部分页面仅验证 loading/ready/empty）
- 知识库（T7）与设置（T8）的逐面板真实数据核验
- 测试数据清理：验收运行（intent 前缀"验收运行"/"真实告警调查"）与测试报告
- G1-G6 逐项判定与最终验收结论

---

## 收尾更新（2026-09-14 18:40）

- acceptance-report.md 全量刷新：结论改为"未通过最终验收（G5 FAIL）"、
  缺陷表补全 D15-D23（9 个）、G1-G6 判定更新、§7 剩余项收敛为 G5 缺口 +
  P1 注记、§9 阶段二汇总新增
- 组件镜像同步：query-run-dispatch / query-alert-eval 更新至 d21（与
  query-api-http 一致）
- 测试数据清理完成：验收 Run/事件/证据/工具运行/假设/报告/批测 Run 全部
  删除（带前缀可识别），保留历史数据；deepflow-grafana 恢复 1/1 并清注解
- 证据包 119 文件；G1-G6 判定见 g1-g6-verdict.md（G5 FAIL 维持）
