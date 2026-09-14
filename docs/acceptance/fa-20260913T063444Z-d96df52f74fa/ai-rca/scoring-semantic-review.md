# 评分模型语义评审提案与判定（v1）

评审输入：规范第 11 章阈值与结论等级定义、rca_engine 评分代码、校准迭代
#1-#6 实测数学分解、受控注入实测数据。评审原则：所有语义增强必须可追溯
到规范条文（"候选原因必须附支持证据与反证"、"Confirmed 精确率 100%"、
"证据不足不得输出 Confirmed"），不放阈值、不删断言。

---

## D-1 症状即根因（self-root）的合法性

**问题**：deployment 配置错误（坏镜像/资源限制/探针错误）时 deployment 自身
就是根因，但 root==symptom → propagation_paths=[] → causal_path_available
=false → confirmed 硬门禁拒绝 → X1-X5 场景永远拒答。

**数学**：self-root 的 topology=0（hops=0），score 上限
0.16(temporal)+0.15(anomaly)+0.05(trace) ≈ 0.36-0.55 < 0.65，
即使 3 类证据也无法达 probable —— 当前模型对配置错误类根因**结构性盲区**。

**选项**：
- A 维持硬门禁 → 配置错误场景永远无法输出结论（不可接受，覆盖缺口）
- B 分档判定（推荐）：self-root 且**直接因果证据**（change 或
  kubernetes_event 绑定症状）+ 独立自有类别 ≥2 + temporal>0 → 允许
  confirmed；仅 events 单类 → probable；再弱 → insufficient
- C 全面放宽 valid_paths → 放水，否决（会让无因果解释的自根因通过）

**判定：采纳 B。** 依据：change 证据与 K8s Warning 事件是直接因果事实
（变更引发故障 / 平台异常事件），规范要求"候选原因附支持证据与反证"，
B 以"直接因果类别 + 独立类别数"满足该要求，同时保留传播路径门禁对
**无直接因果的自根因**的拒绝。

## D-2 症状层告警对症状候选的 anomaly 计入

**问题**：alert（deployment 不可用）绑定症状，抬高分但它是症状信号。

**判定：维持计入，但 self-root confirmed 必须满足 D-1 的直接因果条件。**
依据：告警确为症状层事实，排除它会丢失 temporal/anomaly 信息；D-1 的
直接因果门禁已阻断"仅凭被告知有问题就自证为根因"。

## D-3 传播支持证据的类别计数

**判定：维持不参与 independent_categories。** 依据：传播支持是路径佐证，
不是独立来源；计入会让单信号夸大为多源（违反"独立证据"语义）。
传播路径本身已是 confirmed 必要条件（causal_path_available）。

## D-4 阈值 0.8/0.65 适配性

**判定：维持，self-root 走 D-1 分档。** 数学：三类信号 K8s 场景
（events+alert+trace degraded+传播路径）= 0.2+0.2+0.15+0.05 = 0.60-0.66
→ probable 可达；confirmed 0.8 需要 co_failure/change 加成 —— 与
"Confirmed 精确率 100%" 的高置信要求一致。实测 0.8 难达属于模型保守度，
不构成放水理由；Top-1 指标以 probable 命中计（见 D-5）。

## D-5 Top-1 指标定义

**判定：Top-1 命中 = run.root_cause 投影命中（confirmed/supported 级）
或 candidate_roots[0] ∈ ground truth 且 causal_path_available。**
依据：Supported 级候选也是"结论"的一部分（阶梯设计）；纯 insufficient
但 candidate_roots[0] 命中且无路径的不计命中（防止"蒙对"）。

## 实施项（按 D-1 B）

engine.py self-root 分支：confirmed 需 direct_causal（change/k8s_event）
+ 独立自有类别 ≥2 + temporal>0；self-root 无 direct_causal → probable
（降级不拒答）；非 self-root 维持原门禁。回归测试 + 注入验证（X1）。
