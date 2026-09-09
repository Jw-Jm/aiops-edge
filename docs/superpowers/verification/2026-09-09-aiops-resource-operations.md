# AIOps 资源运维重构验证记录

日期：2026-09-09  ·  分支：`main`

## 已完成的自动验证

- `cd observability-frontend && npm run test:run -- --reporter=dot`：通过（57 个测试文件、117 个测试）。
- `cd observability-frontend && npm run build`：通过（TypeScript + Vite production build）。
- `cd ai-apm-query-go && go test ./internal/api ./internal/graph ./internal/bootstrap`：通过。
- 五条 Playwright 主链均在 1440×900、1280×720、1024×768 通过；运行时无匹配资源/图谱数据时均显示明确 empty/error/forbidden 状态。
- 知识图谱展示模型验证：80 节点 / 200 边为画布预算，超出节点按类型聚合并显示省略数量；原始后端数据不被该视觉预算误报为完整。
- 最终发布：Helm revision `38`，前端镜像 `observability-frontend@sha256:4f760f42e792af3ceac5c8084433ea099ee4de9b7496198afb53c8a32b88f07b`，对应 main 提交 `f8ad8d5cf167`。

## 视口验收矩阵

| 页面主链 | 1440×900 | 1280×720 | 1024×768 | 证据目录 |
| --- | --- | --- | --- | --- |
| 工作台 → 调查草稿 | PASS | PASS | PASS | `test-results/nonllm-20260909T044314Z/screenshots/overview--*` |
| 资源中心 → typed 资源 | PASS | PASS | PASS | `test-results/nonllm-20260909T044324Z/screenshots/resources--*` |
| 关系探索 → 图谱 | PASS | PASS | PASS | `test-results/nonllm-20260909T044333Z/screenshots/graph--*` |
| 调查草稿 → Run 快照 | PASS | PASS | PASS | `test-results/nonllm-20260909T044513Z/screenshots/investigation--*` |
| Run → 处置审计 | PASS | PASS | PASS | `test-results/nonllm-20260909T044522Z/screenshots/actions--*` |

执行命令：

```bash
node tests/manual-e2e/ia-workbench.js
node tests/manual-e2e/ia-resources.js
node tests/manual-e2e/ia-graph.js
node tests/manual-e2e/ia-investigation.js
node tests/manual-e2e/ia-actions.js
```

默认不设置 `RUN_E2E_MUTATIONS=1`，因此只验证只读链路；审批和 Run 创建的真实写操作必须由授权人员显式开启。

## 视觉检查清单

- [x] 三档视口无横向溢出，1024–1279px 调查左右栏降级为可用布局。
- [x] 资源身份、集群、窗口、状态和数据来源有清晰层级；无 `dev/staging` 或全局 Namespace 选择器。
- [x] loading、empty、error、partial、stale、forbidden 六态尺寸稳定且文案不把缺失数据说成健康。
- [x] 图谱节点类型/图标/标签、关系颜色、聚合数量、图例和等价关系列表一致。
- [x] RawDataPanel 默认折叠，只读且超过 200KB 明确截断。

## 已知非阻塞限制

- 图谱 80/200 只约束画布渲染；服务端分页/深度参数仍由 Query API 的实际数据源决定。
- 测试环境无匹配资源或 Graph Context 时，主链应显示明确空态/不可用态，不生成演示数据。

## 回滚点

- 发布前保留 `main` 当前提交及部署 revision。
- 镜像发布后记录 image digest；验收失败时恢复上一 deployment revision 与上一 digest，不在生产页面临时修补。
