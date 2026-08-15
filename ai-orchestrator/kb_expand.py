#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""知识库扩充：按 12 个领域追加新案例到 knowledge_cases.json（保留原有 42 条）。
运行后可用 knowledge_seed.py 增量导入（按 symptom 去重）。
"""
import json
import os

NEW_CASES = [
    # ============ 1. 网络 / DeepFlow 深度观测 ============
    {"service": "network", "symptom": "服务间调用延迟突增，P95 从 10ms 涨到 500ms 以上，调用量未明显变化，拓扑图中延迟等级变为 slow",
     "root_cause": "网络链路拥塞、跨节点/跨集群访问经过低速网络、eBPF 观测显示某一段网络延迟异常（重传/丢包升高）、或对端服务所在节点网络带宽打满",
     "plan": "1. 在 DeepFlow 拓扑中查看延迟分段，定位是哪一段链路（client→server）变慢；2. 查看该链路 TCP 重传率与丢包率（node_exporter tcp_retrans/segs）；3. 检查节点网卡带宽占用（node_network_transmit_bytes/rate）；4. 检查是否存在跨集群/跨 AZ 调用（看 cluster_id 标签）；5. 对带宽打满的节点限流或扩容，对跨网调用改内网路由"},
    {"service": "network", "symptom": "TCP 重传率持续升高，客户端大量超时，应用错误日志出现 connection reset by peer",
     "root_cause": "网络拥塞导致丢包、MTU 不一致、防火墙/安全组丢弃、对端连接数打满触发 RST、或网卡故障",
     "plan": "1. 用 node_exporter tcp_retrans_segs/tcp_out_segs 计算重传率，定位异常节点；2. 检查对端服务的连接数（ss -s、连接数监控），确认是否达到上限；3. 检查网络设备（SNMP）端口错包/丢包计数；4. 核对 MTU 与防火墙策略；5. 临时降级为短连接+重试策略缓解，再根治链路"},
    {"service": "network", "symptom": "跨集群调用偶发超时，链路追踪显示 span 之间出现长时间 gap（几十秒），但各 span 自身耗时正常",
     "root_cause": "网络代理/网关超时、DNS 解析慢、跨集群网络不稳定（丢包重传）、或安全扫描/限流设备周期性介入",
     "plan": "1. 对比不同集群/实例的调用延迟分布，确认是否跨集群才超时；2. 检查 DNS 解析耗时（应用侧 dns_lookup 指标）；3. 查看网关/代理（如 ingress）日志中的超时与限流记录；4. 增加客户端超时与重试，服务端加大请求队列；5. 长期：跨集群调用改异步化或就近接入"},
    {"service": "network", "symptom": "连接数打满，新建连接失败，报 too many open files 或 cannot assign requested address",
     "root_cause": "连接泄漏（未关闭连接）、并发过高超过系统 fd 上限、TIME_WAIT 堆积、或服务端 backlog 队列满",
     "plan": "1. ulimit -n 与 /proc/sys/net/core/somaxconn 检查系统上限；2. ss -s 查看 TIME_WAIT 数量，调大 net.ipv4.tcp_tw_reuse/tcp_fin_timeout；3. 检查应用是否泄漏连接（连接池未回收）；4. 调大服务端 max_connections 与队列；5. 连接池加健康检查与自动回收"},
    # ============ 2. ClickHouse 存储层 ============
    {"service": "clickhouse", "symptom": "ClickHouse 查询变慢，trace_spans 查询从毫秒级变秒级，system.query_log 显示部分查询扫描大量分区",
     "root_cause": "查询未命中分区裁剪（date/time_bucket 条件缺失）、数据倾斜、parts 数过多需要 merge、或磁盘 IO 变慢",
     "plan": "1. EXPLAIN 查看分区裁剪是否生效；2. 检查 system.parts 中 parts 数量与大小，触发 OPTIMIZE 合并；3. 确认查询是否带 date/time_bucket 过滤条件；4. 检查磁盘 IO（iostat）与剩余空间；5. 对高频查询建物化视图或预聚合表"},
    {"service": "clickhouse", "symptom": "ClickHouse 磁盘空间快速增长，trace/log 数据占满磁盘，写入开始失败",
     "root_cause": "TTL 未生效（TTL 设置错误或 merge 未触发）、数据写入量超过预期、或旧数据未按天分区清理",
     "plan": "1. 确认表 TTL 定义与分区策略（trace_spans/log_records 应为 30 天 TTL）；2. 手动执行 OPTIMIZE 触发 TTL 删除；3. 检查是否关闭了 TTL 或修改了存储策略；4. 清理临时/系统表（system.* 只保留必要部分）；5. 长期：降低采样率或延长/缩短 TTL 平衡成本"},
    {"service": "clickhouse", "symptom": "ClickHouse 写入报错 no space left on device 或 too many parts，ingest 采集任务堆积",
     "root_cause": "磁盘满、parts 数超过 max_parts_to_insert 阈值、写入批次过大、或 merge 跟不上写入速度",
     "plan": "1. 检查磁盘空间并清理；2. 查看 system.parts 数量，临时调大 max_parts_to_insert；3. 降低 ingest 写入批次大小或降低采样率；4. 确认 merge 线程池未被占满；5. 长期：扩容磁盘或加副本分担"},
    {"service": "clickhouse", "symptom": "使用 ReplacingMergeTree 的表（trace_spans）查询出现重复数据，同一 trace_id 出现多行",
     "root_cause": "ReplacingMergeTree 的 dedupe 依赖后台 merge 完成，近期写入的数据未被合并，或 version 列设置导致旧版本未被淘汰",
     "plan": "1. 确认表以 (trace_id, span_id) 为主键排序且 version 合理；2. 查询用 FINAL 关键字或在应用层去重；3. 手动 OPTIMIZE 触发合并观察；4. 长期：在查询层按 trace_id+span_id 去重（argMax）"},
    # ============ 3. VictoriaMetrics / VictoriaLogs ============
    {"service": "victoriametrics", "symptom": "VictoriaMetrics 指标出现断点（gap），一段时间内查询不到数据，抓取目标 down",
     "root_cause": "抓取配置变更、目标 pod 重启/迁移、vmagent 或 VM 自身 OOM 重启、或磁盘写入失败",
     "plan": "1. 查 /api/v1/targets 看抓取状态与错误；2. 检查 VM 自身内存/磁盘（指标断点常伴随 OOM）；3. 检查 vmagent 抓取间隔与超时配置；4. 对重要指标加 recording rule 降采样保底；5. 确认 K8s 服务发现（kubernetes_sd）正常"},
    {"service": "victoriametrics", "symptom": "VictoriaMetrics 磁盘快速增长，retention 设置未生效，数据长期不清理",
     "root_cause": "retentionPeriod 配置缺失或过短/过长、高基数指标（cardinality 爆炸）、或 downsampling 未配置",
     "plan": "1. 检查 retentionPeriod 配置并确认生效；2. 用 /api/v1/status/tsdb 分析高基数 label 与 metric；3. 对高基数 label 做 relabel 删除或聚合；4. 配置 downsampling 降采样历史数据；5. 清理无用 job 的指标"},
    {"service": "victorialogs", "symptom": "VictoriaLogs 日志检索变慢或超时，日志量大时查询秒级无响应",
     "root_cause": "查询未加时间范围导致全量扫描、日志量增长过快未配置 retention、或磁盘 IO 瓶颈",
     "plan": "1. 查询强制加 _time 时间范围过滤；2. 检查日志 retention 配置；3. 按 stream/namespace 合理分流降低单流日志量；4. 检查磁盘 IO 与文件数（inode）；5. 对高频查询建 stream 过滤词"},
    # ============ 4. 服务故障模式（RED 指标） ============
    {"service": "application", "symptom": "服务错误率突增但调用量持平，错误集中在 5xx，日志显示下游调用超时",
     "root_cause": "下游依赖服务变慢/不可用（超时传导）、依赖的连接池耗尽、或下游发布故障",
     "plan": "1. 拓扑图查看该服务下游依赖与错误率；2. 链路追踪看失败 span 的服务与耗时；3. 检查下游服务指标（RED）与部署状态；4. 加熔断/降级/快速失败保护；5. 下游恢复后观察错误率回落"},
    {"service": "application", "symptom": "服务 P95 延迟劣化，错误率不高但用户可感知变慢，慢调用集中在某几个端点",
     "root_cause": "某端点存在慢查询（DB/Redis）、锁竞争、或串行调用下游导致耗时叠加",
     "plan": "1. 链路追踪按端点聚合 P95，定位慢 span；2. 检查慢 span 是 DB 查询（db_statement）还是外部调用；3. 对慢 SQL 加索引/优化；4. 检查锁等待（Redis/DB 锁）；5. 评估并行化或缓存热点数据"},
    {"service": "application", "symptom": "调用量骤降（业务量断崖），但服务本身健康指标正常，无告警触发",
     "root_cause": "上游流量被拦截（网关/负载均衡/防火墙）、DNS 变更、上游发布故障、或流量调度变更",
     "plan": "1. 对比网关/入口的流量指标确认入口是否还有流量；2. 检查 DNS 解析与上游路由配置变更；3. 检查安全策略/限流（WAF/防火墙）；4. 对比服务拓扑中上游调用次数变化；5. 回滚相关配置变更"},
    {"service": "application", "symptom": "服务出现周期性错误（每隔固定间隔报错），错误率呈锯齿状波动",
     "root_cause": "定时任务/GC/备份导致资源竞争、连接池周期性回收失败、或下游定时任务影响",
     "plan": "1. 按时间聚合错误率看周期与业务时段关联；2. 检查应用 GC/定时任务/连接池回收配置；3. 链路追踪看周期性错误 span 的特征；4. 错峰执行定时任务；5. 观察修复后锯齿是否消失"},
    # ============ 5. 容量 / 资源 ============
    {"service": "capacity", "symptom": "容量预测显示 CPU 将在 48 小时内触达阈值（ETT 缩短），当前使用率持续上升",
     "root_cause": "业务增长或异常流量导致资源使用率单调上升，当前容量不足以支撑增长趋势",
     "plan": "1. 查看容量预测趋势与 ETT，确认是持续增长还是短期波动；2. 分析增长来源（新业务/数据量/配置不当）；3. 提前扩容节点或调整配额；4. 优化资源使用（降采样、缓存、压缩）；5. 设置容量告警阈值提前预警"},
    {"service": "capacity", "symptom": "磁盘使用率接近阈值（>85%），但日志/数据仍在增长，ETT 已触发",
     "root_cause": "日志/指标/数据保留期过长、或增长速率超预期",
     "plan": "1. 确认各数据源磁盘使用分布；2. 检查 TTL/retention 是否生效；3. 清理历史数据或归档；4. 评估磁盘扩容或压缩；5. 设置磁盘水位告警"},
    {"service": "capacity", "symptom": "内存使用率持续高位（>90%）但 CPU 正常，服务开始出现 OOMKilled",
     "root_cause": "内存泄漏、缓存/连接池配置过大、或 Pod 内存 limit 设置不合理",
     "plan": "1. 检查内存使用趋势与 OOM 事件；2. 用 heap dump/pprof 定位泄漏对象；3. 检查缓存与连接池配置上限；4. 调整内存 limit 或扩内存；5. 加内存监控告警"},
    # ============ 6. SNMP 网络设备 / IPMI 硬件 ============
    {"service": "snmp", "symptom": "交换机端口错包/丢包计数持续增长，通过该端口的服务出现偶发延迟与重传",
     "root_cause": "端口物理故障/劣化（光模块、网线）、双工不匹配、或广播风暴",
     "plan": "1. 用 SNMP 查看端口 in/out errors、discards、CRC 错误计数；2. 检查端口双工/速率协商是否正常；3. 检查光模块光功率（DOM）；4. 重启端口或更换线缆/模块；5. 配置端口错误率告警"},
    {"service": "snmp", "symptom": "SNMP 设备采集失败或超时，设备列表显示不可达",
     "root_cause": "SNMP community 变更、设备 IP 变更、防火墙拦截 161 端口、或设备 SNMP 服务未启用",
     "plan": "1. snmpwalk 手工验证连通性与 community；2. 检查设备 IP/路由变更；3. 检查防火墙放行 UDP 161；4. 确认设备 SNMP 服务状态；5. 更新平台设备配置"},
    {"service": "hardware", "symptom": "服务器 IPMI 传感器显示温度过高或风扇转速异常，硬件健康状态降级",
     "root_cause": "风扇故障、散热风道堵塞、环境温度过高、或传感器误报",
     "plan": "1. 查看 IPMI 传感器读数（温度/风扇/电压）；2. 检查服务器物理环境（机房温度/风道）；3. 检查故障风扇并更换；4. 清灰维护；5. 配置温度告警阈值提前介入"},
    {"service": "hardware", "symptom": "服务器电源模块异常或冗余电源丢失，IPMI SEL 事件持续上报",
     "root_cause": "电源模块硬件故障、电源线接触不良、或供电输入异常",
     "plan": "1. 查看 IPMI SEL 事件日志定位故障电源；2. 检查电源指示灯与供电线路；3. 更换故障电源模块（注意冗余）；4. 验证切换是否正常；5. 配置电源事件告警"},
    # ============ 7. K8s 深入（工作负载级） ============
    {"service": "kubernetes", "symptom": "HPA 扩缩容频繁抖动，Pod 数量在短时间内反复增减",
     "root_cause": "指标波动大导致扩缩容振荡、min/max 设置过窄、或 HPA 行为策略（behavior）未配置稳定窗口",
     "plan": "1. 查看 HPA 状态与指标（kubectl describe hpa）；2. 增大 stabilizationWindowSeconds 或设置冷却时间；3. 扩大 min/max 缓冲范围；4. 检查指标采集稳定性；5. 对关键业务配置按负载预测扩缩容"},
    {"service": "kubernetes", "symptom": "Pod 长时间 Pending 无法调度，报 Insufficient cpu/memory 或 node selector 不匹配",
     "root_cause": "集群资源不足、节点污点/亲和性限制、PVC 未就绪、或节点 NotReady",
     "plan": "1. kubectl describe pod 查看调度事件；2. 检查集群节点资源余量（capacity/allocatable）；3. 检查污点容忍与 nodeSelector/亲和性；4. 确认 PVC 状态（Bound）；5. 扩容节点或调整调度策略"},
    {"service": "kubernetes", "symptom": "Pod 频繁重启（CrashLoopBackOff），重启次数超过 3 次触发告警",
     "root_cause": "应用启动失败（配置/依赖）、探针失败导致重启、OOMKilled、或就绪探针配置错误",
     "plan": "1. kubectl logs 查看崩溃前日志；2. 查看上次退出码与原因（OOMKilled=137）；3. 检查探针（liveness/readiness）配置与阈值；4. 检查资源配置（limit 过小触发 OOM）；5. 修复后观察重启计数回落"},
    {"service": "kubernetes", "symptom": "PVC 使用率超过 85% 触发容量告警，应用开始报磁盘写入失败",
     "root_cause": "PVC 容量规划不足、日志/临时文件未清理、或应用数据增长超预期",
     "plan": "1. 查看 PVC 用量与扩容支持类型（ReadWriteOnce 需重建 Pod）；2. 清理应用日志与临时文件；3. 评估 PVC 扩容（storageClass 支持扩容则直接扩）；4. 配置自动清理任务；5. 长期：分卷或换更大容量"},
    # ============ 8. 告警处置 SOP（knowledge 类型） ============
    {"service": "alerts", "type": "knowledge", "tags": "sop,alert,error_rate", "title": "告警处置 SOP：服务错误率过高（trace_error_rate）",
     "symptom": "告警处置 SOP：服务错误率过高（trace_error_rate）\n当触发'服务错误率过高'告警（阈值 5%）时，按以下步骤处置：1) 打开服务全景查看该服务错误率与调用量趋势；2) 链路追踪按该服务过滤，查看失败 span 的状态码与耗时；3) 检查服务依赖（拓扑下游）是否异常；4) 查看该服务最近日志（ERROR 级别）定位报错类型；5) 若为下游依赖故障，启动熔断/降级并联系下游负责人；6) 处置完成后观察错误率回落并确认告警恢复",
     "root_cause": "标准处置流程",
     "plan": "1. 确认告警对象与触发值；2. 按链路→日志→依赖顺序排查；3. 执行处置并验证恢复"},
    {"service": "alerts", "type": "knowledge", "tags": "sop,alert,kubernetes", "title": "告警处置 SOP：Deployment 不可用（unavailable_replicas）",
     "symptom": "告警处置 SOP：Deployment 不可用（unavailable_replicas）\n当触发'Deployment 不可用'告警时：1) kubectl get deploy -A 定位不可用 Deployment；2) kubectl describe deploy 查看滚动更新状态；3) 检查 Pod 状态（CrashLoopBackOff/OOM/ImagePullBackOff）；4) 查看最近事件与镜像版本；5) 回滚或修复后确认副本恢复；6) 确认告警自动恢复（resolved）",
     "root_cause": "标准处置流程",
     "plan": "1. 定位不可用 Deployment；2. 检查 Pod 与事件；3. 回滚/修复；4. 验证恢复"},
    {"service": "alerts", "type": "knowledge", "tags": "sop,alert,kubernetes", "title": "告警处置 SOP：Pod 频繁重启（pod_restarts）",
     "symptom": "告警处置 SOP：Pod 频繁重启（pod_restarts）\n当触发'Pod 频繁重启'告警（阈值 3 次）时：1) 定位重启 Pod；2) kubectl describe pod 查看重启原因（OOMKilled/探针失败/崩溃）；3) 查看退出码（137=OOM，其余看日志）；4) 检查资源 limit 与探针配置；5) 修复后观察重启计数是否停止增长；6) 确认告警恢复",
     "root_cause": "标准处置流程",
     "plan": "1. 定位 Pod；2. 查原因；3. 修复；4. 验证恢复"},
    # ============ 9. 变更 / 发布风险库（knowledge 类型） ============
    {"service": "change", "type": "knowledge", "tags": "change,risk,deploy", "title": "变更风险：应用版本升级后出现错误率上升",
     "symptom": "变更风险：应用版本升级后出现错误率上升\n发布新版本后错误率上升时：1) 立即对比新旧版本差异（配置/依赖/代码）；2) 检查新版本是否引入新的外部依赖或配置项；3) 优先回滚到上一稳定版本；4) 保留现场（日志/链路）供分析；5) 修复后在灰度环境验证再全量发布",
     "root_cause": "发布风险控制",
     "plan": "1. 回滚；2. 分析差异；3. 灰度验证；4. 再发布"},
    {"service": "change", "type": "knowledge", "tags": "change,risk,config", "title": "变更风险：配置变更导致服务行为异常",
     "symptom": "变更风险：配置变更导致服务行为异常\n配置变更（环境变量/配置文件/参数）后服务异常时：1) 检查配置变更记录（审计日志/发布记录）；2) 对比变更前后配置差异；3) 回滚配置变更；4) 验证配置是否被正确加载（是否生效）；5) 配置变更建议走评审与灰度",
     "root_cause": "配置变更风险控制",
     "plan": "1. 定位变更；2. 回滚；3. 验证；4. 建立配置评审流程"},
    # ============ 10. 修复命令白名单知识（knowledge 类型） ============
    {"service": "shell", "type": "knowledge", "tags": "shell,whitelist,command", "title": "安全命令白名单：平台可安全执行的排查命令",
     "symptom": "安全命令白名单：平台可安全执行的排查命令\n以下命令为只读/低风险排查命令，可授权 AI 自动执行：kubectl get/describe（资源查看）、kubectl logs（日志查看）、kubectl top（资源占用）、kubectl get events（事件）、ss/netstat（连接查看）、df/free/top（系统资源）、curl 健康检查、ping/traceroute（连通性）。写操作命令（删除/重启/修改）必须人工审批",
     "root_cause": "命令风险分级",
     "plan": "1. 只读命令可自动执行；2. 写命令必须审批；3. 命令输出应脱敏"},
    # ============ 11. SLO / 容量最佳实践（knowledge 类型） ============
    {"service": "slo", "type": "knowledge", "tags": "slo,best-practice", "title": "SLO 最佳实践：目标设定与烧毁率解读",
     "symptom": "SLO 最佳实践：目标设定与烧毁率解读\n1) 可用性目标建议 99.9% 起步，关键链路 99.99%；2) 延迟 SLO 建议使用 P95/P99，而非均值；3) 烧毁率 > 1 表示错误预算消耗速度超预期，应立即处理；4) SLO 应覆盖核心用户链路，非全量服务；5) 每月复盘 SLO 达成情况并调整目标",
     "root_cause": "SLO 配置最佳实践",
     "plan": "1. 按服务重要性分级设定 SLO；2. 关联烧毁率告警；3. 定期复盘"},
    # ============ 12. 业务场景案例（端到端） ============
    {"service": "application", "symptom": "订单服务（checkout）错误率 100%，调用量骤降，下游 payments 服务同样异常",
     "root_cause": "业务链路故障传导：checkout 依赖 payments，payments 故障（或故障注入）导致 checkout 全部失败；Redis 频繁重启加剧会话/缓存依赖失败",
     "plan": "1. 拓扑确认 checkout→payments 依赖链；2. 检查 payments 错误率与日志；3. 检查 Redis 重启原因（OOM/探针）；4. 对 checkout 加熔断避免全量失败；5. 恢复 payments/Redis 后验证链路恢复"},
    {"service": "application", "symptom": "登录/会话服务出现周期性 401/超时，用户间歇性无法登录",
     "root_cause": "会话存储（Redis）不稳定或重启导致会话丢失、或认证服务依赖的下游超时",
     "plan": "1. 检查 Redis 稳定性（重启次数/内存）；2. 查看认证服务错误日志与链路；3. 检查会话过期配置与 TTL；4. 修复 Redis 后观察登录成功率恢复"},
]

def main():
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "data", "knowledge_cases.json")
    with open(path, "r", encoding="utf-8") as f:
        existing = json.load(f)
    old_count = len(existing)
    # 按 (service, symptom) 去重追加
    seen = {(c.get("service", ""), c.get("symptom", "")[:60]) for c in existing}
    added = []
    for c in NEW_CASES:
        key = (c.get("service", ""), c.get("symptom", "")[:60])
        if key in seen:
            continue
        seen.add(key)
        added.append(c)
    existing.extend(added)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(existing, f, ensure_ascii=False, indent=1)
    print(f"原有 {old_count} 条，新增 {len(added)} 条，当前共 {len(existing)} 条")
    from collections import Counter
    print("service 分布:", dict(Counter(c.get('service') for c in added)))

if __name__ == "__main__":
    main()
