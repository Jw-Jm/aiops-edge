# 批5：B4 Monitor 完整看板 + C1 工具分级与统一审批闸门 实施计划

**Goal:** Monitor 看板升级为完整可配置看板系统（echarts + 面板 CRUD + 布局 + 持久化）；完善工具分级 + 统一审批闸门。

**Tech Stack:** Go（query-api 面板 CRUD）、Python（ai-orchestrator 工具分级/闸门）、React（echarts-for-react 看板）、pytest/go test

---

## Global Constraints

- 看板面板元数据存 MySQL（`dashboard_panels` 表，query-api 拥有），复用 hasColumn/幂等迁移
- 工具分级：safe 直接执行；mutating+requires_approval 走审批；dangerous 强制审批
- 复用现有 Approvals/Tasks/恢复白名单，不新建审批系统
- 看板拖拽用 CSS grid（不引重库），12 栅格 span 控制
- echarts-for-react 复用 Overview 模式
- 零回归：现有 Monitor 占位替换为真实看板；Approvals/Tasks 不受影响

---

### Task 1: B4 后端 — dashboard_panels 表 + 面板 CRUD API

**Files:** `ai-apm-query-go/internal/api/dashboard.go`(新)、`internal/store/dashboard.go`(新)、`internal/store/mysql.go`

**Interfaces:**
- Consumes: MySQL, `requireRole`(admin 写)
- Produces: `DashboardPanel` 结构 + CRUD API `/api/v1/dashboard/panels`

- [ ] **Step 1: 写失败测试** `internal/api/dashboard_test.go`（TestPanelCRUD）

```go
func TestPanelCRUD(t *testing.T) {
	h := newTestHandler()
	// create
	got := h.createPanel(panel{Title:"rate", Query:"sum(rate(...))", ChartType:"line", GridW:6})
	// list
	panels := h.listPanels()
	if len(panels) != 1 || panels[0].Title != "rate" { t.Fatal }
	// update grid
	// delete
}
```

- [ ] **Step 2: 运行测试验证失败**
Run: `cd ai-apm-query-go && go test ./internal/api/ -run TestPanelCRUD -v`
Expected: FAIL

- [ ] **Step 3: 实现 store `dashboard.go` + `mysql.go` 建表**

```go
// store/dashboard.go
type DashboardPanel struct {
	ID, Title, Query, ChartType, GridX, GridY, GridW, GridH, Sort, Enabled, CreatedAt
}
func (d *DashboardPanelDAO) List() ([]DashboardPanel, error)
func (d *DashboardPanelDAO) ReplaceAll(panels []DashboardPanel) error  // upsert + 删差异
```
mysql.go EnsureSchema 加 `dashboard_panels` 建表 + 种子 4 面板。

- [ ] **Step 4: 实现 api `dashboard.go`**（GET/POST/PUT/DELETE，写 admin 守卫，list 公开读）

- [ ] **Step 5: 运行测试验证通过**
Run: `go test ./internal/api/ -run TestPanelCRUD -v` → PASS

- [ ] **Step 6: 提交**
```bash
git add ai-apm-query-go/internal/api/dashboard.go ai-apm-query-go/internal/store/dashboard.go ai-apm-query-go/internal/store/mysql.go ai-apm-query-go/internal/api/dashboard_test.go
git commit -m "feat(batch5): dashboard_panels 表 + 面板 CRUD API"
```

---

### Task 2: B4 前端 — Monitor 完整看板（echarts + 面板自定义 + 布局）

**Files:** `observability-frontend/src/pages/Monitor/index.tsx`、`api/client.ts`

- [ ] **Step 1: 前端 tsc（改前基线）**
Run: `cd observability-frontend && npx tsc --noEmit -p tsconfig.json 2>&1 | tail -3`

- [ ] **Step 2: client.ts 加面板 API**（listDashboardPanels/createPanel/updatePanel/deletePanel）

- [ ] **Step 3: Monitor/index.tsx 重写**
  - 加载面板元数据（后端）+ 渲染 echarts（line/bar/area/gauge/table 按 ChartType）
  - 面板自定义：新增/编辑（Modal 表单：标题/查询/图表类型/span）/删除
  - 布局：CSS grid 12 栅格，span 控制宽度，上/下移动排序
  - 刷新 + 时间范围（默认 1h）
  - 复用 AppEmpty 空态 + echarts-for-react

- [ ] **Step 4: 前端 tsc 验证**
Run: `npx tsc --noEmit -p tsconfig.json 2>&1 | tail -3` → 无新增错误

- [ ] **Step 5: 提交**
```bash
git add observability-frontend/src/pages/Monitor/index.tsx observability-frontend/src/api/client.ts
git commit -m "feat(batch5): Monitor 完整看板（echarts + 面板CRUD + 布局）"
```

---

### Task 3: C1 工具分级补全 + 统一审批闸门

**Files:** `ai-orchestrator/skills/vm_ops.py`、`ai-orchestrator/skills/automation.py`、`ai-orchestrator/execution_gate.py`(新)、`ai-orchestrator/skill_registry.py`(闸门集成)

- [ ] **Step 1: 写失败测试** `tests/test_execution_gate.py`

```python
def test_safe_tool_direct():
    # execute_shell 分级 dangerous
def test_mutating_needs_approval():
    # vm_operate 分级 mutating + 需审批
def test_dangerous_forced_approval():
    # execute_shell 强制审批
def test_gate_blocks_without_approval():
    # 未审批调用 mutating/dangerous 被拒
```

- [ ] **Step 2: 运行测试验证失败** → FAIL

- [ ] **Step 3: 实现 `execution_gate.py`**

```python
def check_tool_executable(tool, approved: bool) -> (bool, str):
    """统一执行闸门: safe直接; mutating+dangerous 必须审批"""
```

- [ ] **Step 4: 补分级**：`vm_operate` 标 `cls_="mutating"`、`execute_shell` 标 `cls_="dangerous"`

- [ ] **Step 5: 集成闸门**到工具执行入口（ToolRegistry/ToolDef 调用前 check）

- [ ] **Step 6: 运行测试验证通过** → PASS
Run: `python3 -m pytest tests/test_execution_gate.py tests/test_function_calling.py -q`

- [ ] **Step 7: 提交**
```bash
git add ai-orchestrator/skills/vm_ops.py ai-orchestrator/skills/automation.py ai-orchestrator/execution_gate.py ai-orchestrator/tests/test_execution_gate.py
git commit -m "feat(batch5): 工具分级补全 + 统一执行审批闸门"
```

---

### Task 4: C1 前端 — Skills 页工具分级标识

**Files:** `observability-frontend/src/pages/Skills/index.tsx`

- [ ] **Step 1: 前端 tsc 基线**
- [ ] **Step 2: Skills 工具卡片加 cls 标签**（safe/mutating/dangerous 颜色区分：绿/橙/红）
- [ ] **Step 3: tsc 验证 + 提交**

---

### Task 5: 全量回归 + 部署验证

- [ ] **Step 1: Go 全量测试** `go test ./...`
- [ ] **Step 2: Python 测试** `pytest tests/ -q`
- [ ] **Step 3: 前端 tsc**
- [ ] **Step 4: 构建部署** query-api + orchestrator + frontend
- [ ] **Step 5: 端到端验证**：面板 CRUD、echarts 看板、工具分级标签
- [ ] **Step 6: 提交部署**

---

## 自审

- B4：面板 CRUD + echarts 渲染 + 布局 + 持久化 ✅
- C1：工具分级 + 统一审批闸门 + 前端标识 ✅
- 复用现有（echarts/Approvals/白名单），组件最小化 ✅
