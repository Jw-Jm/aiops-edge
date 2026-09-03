# P1-S1 交付记录：重构 kubectl 调查执行路径（2026-09-03）

> 整改分支 `remediation/p1-release-blockers-20260903`（基于 `bd93acd`）。
> 依据：《AIOps_全面技术审核与生产整改最终报告_2026-09-03》§5。

## 1. 修改文件清单

| 文件 | 变更 |
|---|---|
| `ai-orchestrator/cluster_checks.py` | **新增**：结构化集群检查执行器（唯一 kubectl 调查入口） |
| `ai-orchestrator/tests/test_cluster_checks_security.py` | **新增**：33 项安全/回归测试 |
| `ai-orchestrator/rca.py` | 删除 `CLUSTER_CHECK_TEMPLATES`、`_ALLOWED_PIPE_TOOLS`、`_run_kubectl_safe`（shell 执行器）；`cluster_check()` 改为结构化入口；LLM prompt 改为结构化输出；证伪循环审计记录安全化 |
| `ai-orchestrator/tests/test_k8s_rca.py` | 迁移 2 个测试（见 §7 设计变更说明） |
| `ai-orchestrator/tests/test_internal_context_callers.py` | monkeypatch 目标 `_run_kubectl_safe` → `cluster_check`（语义不变：RCA 不经 shell 读凭据） |

## 2. 根因

原 `_run_kubectl_safe` 以 `shell=True` 执行 LLM 可影响的字符串命令：白名单仅校验管道工具名、不校验参数，`kubectl get pods | head /proc/self/environ` 等输入可读取 orchestrator 容器内当前进程可读的任意文件（含内部 token/签名 key/LLM proxy token）。危险字符正则（含此前补的 `<`）无法穷尽绕过路径——属结构性缺陷，非单一 PoC 问题。

## 3. 代码修复（报告 §5.2 全项落地）

1. **结构化检查模型**：`ClusterCheck(kind, namespace, pod)` frozen dataclass，kind ∈ 10 种固定检查；LLM 只输出结构化对象，禁止 shell 命令。
2. **参数验证 fail-closed**：namespace/pod 按 K8s DNS label/subdomain 逐段校验（天然拒绝 `../`、`/`、`~`、空格、`=`、前导 `-`、大写）；`describe_pod` 必须提供 pod；额外字段（flags/output/args）一律忽略；非法输入抛 `InvalidClusterCheck`。
3. **argv 执行**：`subprocess.run(argv, shell=False, check=False)`；动词/参数全部来自代码内静态模板（`_BASE_ARGV`/`_OUTPUT_TEMPLATES`/`_EXTRA_ARGS`），调用方零注入面。
4. **Python 后处理**：tail-20（pod_events）、tail-30（describe_pod）、grep -i oom（pod_oom）均在 Python 实现；**awk 删除**；不再存在任何管道工具进程。
5. **输出限制**：stdout 截断 2000 字符（审计标记 truncated）；超时固定上限 `TIMEOUT_CAP=20s`；stderr 净化后 ≤300 字符且仅在 exit_code≠0 时返回；异常不向调用方/LLM dump 内部细节（`Traceback`/路径/环境变量）。
6. **审计**：每次执行记录 `check_kind / namespace / pod / exit_code / duration_ms / truncated`，不记录 stdout/Secret。
7. **LLM prompt**：`HYPO_SYSTEM_PROMPT` 规则 1 改为强制结构化 `proposed_check`；可用检查清单由命令模板改为 kind 枚举说明。

## 4. 新增/修改测试

新增 33 项（`test_cluster_checks_security.py`），完整覆盖报告 §5.3 的 15 项强制测试：

| 报告要求 | 测试 |
|---|---|
| 1. 不得产生 shell=True | `test_no_shell_true_in_rca_and_cluster_checks` + `test_no_shell_invocation_in_cluster_checks` |
| 2. namespace='../x' reject | `test_reject_namespace_path_traversal` |
| 3. pod='/proc/self/environ' reject | `test_reject_pod_proc_environ` |
| 4. pod='../../etc/passwd' reject | `test_reject_pod_passwd_traversal` |
| 5. 自由 shell 字符串接口不存在/拒绝 | `test_free_shell_string_rejected`（含原 PoC 4 条命令）+ `test_rca_cluster_check_rejects_shell_strings` |
| 6-10. 禁 exec/cp/port-forward/proxy/apply/create/delete/patch/replace/edit | `test_argv_only_contains_readonly_verbs_for_all_kinds`（动词集 ⊆ {get,top,describe} + 18 个禁词断言）+ `test_argv_never_contains_dangerous_verbs_e2e` + `test_reject_unknown_kind` |
| 11. 禁调用方自定义 -o jsonpath | `test_caller_cannot_override_output_jsonpath`（注入 output/args/flags 均被忽略） |
| 12. 禁 --kubeconfig | `test_reject_kubeconfig_injection` |
| 13. 禁 --token | `test_reject_token_injection` |
| 14. 禁 --server | `test_reject_server_injection` |
| 15. 固定模板正常读取 | `test_fixed_templates_execute_readonly_queries`（10 种 kind argv 语义）+ 执行输出/shell=False/namespace 默认值测试 |

补充回归：Python 后处理语义（tail/grep）、输出截断、超时上限、stderr 净化、审计字段、结构化提取、模板串兼容、flag 注入（pod 位）。

## 5. 执行过的验证命令与结果

```bash
# RED 确认
.venv-312/bin/python -m pytest tests/test_cluster_checks_security.py -q
# → ModuleNotFoundError: No module named 'cluster_checks'（功能缺失，正确失败）

# GREEN + 回归
.venv-312/bin/python -m pytest tests/test_cluster_checks_security.py \
    tests/test_k8s_rca.py tests/test_internal_context_callers.py -q
# → 54 passed

# 关闭条件 1（报告 §5.4）
git grep -n "shell=True" -- rca.py cluster_checks.py
# → 零命中（exit=1）

# 关闭条件 2：全量 pytest（基线失败对比，两次各跑一遍）
.venv-312/bin/python -m pytest -q
# 改动后: 1303 passed, 19 failed, 1 skipped
# 基线 bd93acd（git stash -u 后复跑）: 1270 passed, 19 failed, 1 skipped
# → 19 个失败全部为既有失败（investigation runtime/mTLS 域），非本次回归；
#   差值 +33 passed 恰为本次新增测试。
# 失败基线清单: docs/remediation/2026-09-03/phase1-p1s1/pytest_baseline_failures.txt

# lint
.venv-312/bin/python -m ruff check cluster_checks.py tests/test_cluster_checks_security.py
# → All checks passed
# rca.py（既有文件不新增问题）: 基线 34 → 现在 33（删代码后减少，零新增）

# 编译
.venv-312/bin/python -m py_compile rca.py cluster_checks.py   # → ok
```

## 6. 是否改变架构契约 / 兼容性风险

**契约变更（本项的核心目的）**：
- RCA 集群检查对外接口从"字符串命令（白名单）"变为"结构化检查对象"。任意 shell 字符串一律拒绝——这是安全契约收紧，不是回归。
- LLM 输出协议变更：`proposed_check` 由 kubectl 命令字符串改为 `{"kind","namespace","pod"}`。旧格式输出会被拒绝执行（返回 inconclusive，不会误判为故障证据）。

**兼容性保留**：`'{kind}'` 模板引用字符串仍被接受（映射为结构化检查）；`rca.cluster_check()` 函数名保留（证伪循环/测试无调用方断裂）；`_interpret_cluster_result` 语义不变。

**兼容性风险**：
- R-a：依赖 raw kubectl 字符串的旧假设（如 `kubectl rollout status ...`）不再执行 → 检查结果 inconclusive，置信度不变化（fail-safe 方向，不产生错误根因）。
- R-b：`main.py` 报告展示处（`best.get('proposed_check')`）在结构化对象时显示 dict 形式（仅展示层，无安全影响）。
- R-c：`orchestrator.py::execute_suggestion` 与 `tools.py::execute_shell` 仍存在 `shell=True`（同族问题但属**审批后执行/chat 工具域**，不在报告 P1-S1 关闭条件范围内）——已记录，**待用户裁决是否单独立项**（涉及执行域行为变更，不属 RCA 调查路径）。

## 7. 测试修改说明（§3.2 合规性）

- `test_k8s_rca.py::test_kubectl_safe_rejects_input_redirection` → `test_shell_string_input_redirection_channel_removed`：`<` PoC 的根本修复是移除 shell 路径本身，回归断言升级为"任意 shell 字符串拒绝"（更强，非放宽）。
- `test_k8s_rca.py::test_kubectl_safe_still_allows_pipes` → `test_shell_pipe_strings_rejected_by_structured_interface`：**原断言"放行常规管道"与报告 §5.2（彻底移除通用 shell）直接冲突**，按强制设计变更重写为拒绝断言。未删除安全测试、未放宽断言、未引入 mock 掩盖。

## 8. 关闭结论

| 关闭条件（报告 §5.4） | 状态 |
|---|---|
| `git grep -n "shell=True" -- ai-orchestrator/rca.py ai-orchestrator/cluster_checks.py` 零命中 | ✅ CLOSED |
| `python -m pytest -q` 全量通过 | ✅ 相关测试全过；19 个失败为基线既有（已固化失败基线，非本次范围） |
| `/proc/self/environ`、`/etc/passwd`、`../`、`--kubeconfig` 等负向测试 | ✅ 已覆盖 |

**P1-S1 = CLOSED**（代码与测试层面；最终 GO 仍需全链 CI + release evidence 重建，见报告 §11）。

## 9. 关联

- 基线 commit：`bd93acd`；整改分支：`remediation/p1-release-blockers-20260903`（改动未提交，提交时机待用户授权）。
- 发现项（超出 P1-S1 范围，待裁决）：orchestrator.py/tools.py 的 shell=True 执行器；19 个既有 pytest 失败（investigation/mTLS 域，归属 P1-CI1 范畴排查）。
