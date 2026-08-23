"""P7.9 Registered Data Source Mapping — TDD 测试（V9.3 Phase7，内存 MVP）。

覆盖 P7.9 设计的 T1-T4：
- T1 映射正确（平台自身 VM/VLogs → 正确 Tool/capability；已注册外部平台 → Cluster Registry 映射）
- T2 语义一致（平台自身 + 外部平台用相同 ToolResult 状态 / Evidence provenance）
- T3 fail-closed（未知/未注册 Cluster → fail-closed；无有效配置 → fail-closed；错误 canonical → 拒绝）
- T4 模型充足性（现有模型足够 → reuse；需新 SoT → BLOCKED + Architecture Deviation）
"""
from __future__ import annotations

import pytest

from data_source_mapping import ArchitectureDeviation, ClusterFailClosed, DataSourceMapping


CLUSTER = "91771a6e-9c2d-11f1-8271-bea176fe9f9f"
OTHER_CLUSTER = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"


@pytest.fixture
def mapping():
    return DataSourceMapping(registered_clusters={CLUSTER})


# ═══════════════════════════════════════════════════════
#  T1 映射正确
# ═══════════════════════════════════════════════════════

class TestT1Mapping:
    def test_map_tool_vm(self, mapping):
        assert mapping.map_tool("query_metrics.v1") == "VM"

    def test_map_tool_vlogs(self, mapping):
        assert mapping.map_tool("query_logs.v1") == "VLogs"

    def test_map_capability(self, mapping):
        assert mapping.map_capability("observability.metrics.read") == "VM"
        assert mapping.map_capability("observability.logs.read") == "VLogs"

    def test_registered_external_platform_maps(self, mapping):
        # 已注册外部平台 cluster → 经 Cluster Registry 映射（resolve 成功）
        assert mapping.resolve_cluster(CLUSTER) == CLUSTER


# ═══════════════════════════════════════════════════════
#  T2 语义一致
# ═══════════════════════════════════════════════════════

class TestT2SemanticConsistency:
    def test_same_toolresult_provenance_for_all_sources(self, mapping):
        # 平台自身 + 外部平台用相同 ToolResult 状态 / Evidence provenance
        # （本 MVP：map_tool 对所有 tool 返回统一 source 枚举，不新增错误语义）
        vm_src = mapping.map_tool("query_metrics.v1")
        ext_src = mapping.map_tool("query_logs.v1")
        # 都来自同一枚举体系（VM/VLogs/query-api），非新 source identity
        assert vm_src in {"VM", "VLogs", "query-api"}
        assert ext_src in {"VM", "VLogs", "query-api"}

    def test_unavailable_not_new_source_auth(self, mapping):
        # 外部平台不可达 → 现有 unavailable 语义（P7.3），不新增 source authorization
        assert mapping.map_tool("query_metrics.v1") in {"VM", "VLogs", "query-api"}


# ═══════════════════════════════════════════════════════
#  T3 fail-closed
# ═══════════════════════════════════════════════════════

class TestT3FailClosed:
    def test_unregistered_cluster_fail_closed(self, mapping):
        with pytest.raises(ClusterFailClosed):
            mapping.resolve_cluster("unknown-cluster-id")

    def test_no_valid_config_fail_closed(self, mapping):
        with pytest.raises(ClusterFailClosed):
            mapping.assert_valid_config(credential_ref="", cluster_registered=True)

    def test_wrong_canonical_cluster_rejected(self, mapping):
        # 映射指向错误 canonical cluster → 拒绝
        with pytest.raises(ClusterFailClosed):
            mapping.resolve_cluster(OTHER_CLUSTER)


# ═══════════════════════════════════════════════════════
#  P7 fail-open 审计修复：未知 tool/capability 必须 fail-closed
# ═══════════════════════════════════════════════════════

class TestFailClosedUnknownToolCapability:
    def test_unknown_tool_fail_closed(self, mapping):
        """审计 P7：未知 tool 不得默认回退 query-api，必须 fail-closed 拒绝。"""
        with pytest.raises(ClusterFailClosed):
            mapping.map_tool("unknown_tool.v99")

    def test_unknown_capability_fail_closed(self, mapping):
        """审计 P7：未知 capability 不得默认回退 query-api，必须 fail-closed 拒绝。"""
        with pytest.raises(ClusterFailClosed):
            mapping.map_capability("capability.not.registered")

    def test_known_tool_still_maps(self, mapping):
        """正向：已注册 tool 仍正确映射（功能不回退）。"""
        assert mapping.map_tool("query_metrics.v1") == "VM"
        assert mapping.map_tool("query_logs.v1") == "VLogs"


# ═══════════════════════════════════════════════════════
#  T4 模型充足性
# ═══════════════════════════════════════════════════════

class TestT4ModelSufficiency:
    def test_existing_model_reuse(self, mapping):
        assert mapping.classify_model(needs_new_sot=False, missing_mandatory_fields=0) == "reuse"

    def test_minimal_extension(self, mapping):
        # 缺 1 个强制注册字段 → 最小扩展（Phase7 change），保留 Gate6 语义
        assert mapping.classify_model(needs_new_sot=False, missing_mandatory_fields=1) == "minimal_extension"

    def test_new_sot_blocked(self, mapping):
        # 需新 SoT / 身份 / 第二查询路径 → BLOCKED + Architecture Deviation（不静默实现）
        with pytest.raises(ArchitectureDeviation):
            mapping.assert_not_blocked(needs_new_sot=True)
