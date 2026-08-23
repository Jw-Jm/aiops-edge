"""R2 方案 B — Evidence 身份与去重键的固定规范（FROZEN，三端共享）。

评审阻断项 3（2026-08-21）：
- Evidence 必须保持 `evidence_id: UUID`（实体身份），`provenance_fingerprint: string`（事实去重键）。
- `evidence_id` 由固定 namespace 的 UUIDv5 从 fingerprint 确定性生成。
- 跨 Go / Python / TypeScript 必须可复算同一 fingerprint → 同一 evidence_id。

FROZEN_EVIDENCE_NAMESPACE（固定，不得更改，否则破坏确定性）：
- 用于 UUIDv5(NAMESPACE, fingerprint) 派生 evidence_id。
- 三端硬编码同一值。

fingerprint 字节级规范（canonical serialization）：
- fingerprint = SHA256(canonical_provenance_fields) 的十六进制小写字符串。
- canonical_provenance_fields 按固定顺序拼接，字段间用 NUL('\x00') 分隔：
  source + '\x00' + query_id + '\x00' + resource_id + '\x00' + time_range_start +
  '\x00' + time_range_end + '\x00' + digest + '\x00' + tenant_id + '\x00' +
  cluster_id + '\x00' + run_id
- 缺失值统一规范化为空字符串 ""（None/缺失 → ""）；时间格式统一 ISO-8601 UTC
  "YYYY-MM-DDTHH:MM:SSZ"（无小数秒、无时区偏移、大写 Z）；全部字段按原样小写处理
  （query_id/resource_id 等保持调用方规范值，digest 为十六进制小写）。
- 大小写：digest 一律小写十六进制；UUID 形字段按小写规范形式；时间字符串固定格式。
- 空值：None / 缺失 / "" 全部归一为 ""（不得出现 "None" 文本）。
"""
from __future__ import annotations

import hashlib
from datetime import datetime, timezone
from typing import Any, Dict, Optional
from uuid import UUID

# ── FROZEN namespace（三端共享，勿改）─────────────────────────────────────
FROZEN_EVIDENCE_NAMESPACE = UUID("6f1c3a5e-2b8a-4f3e-9d2c-1a0b3c4d5e6f")
# Y2 PlanStep：用于 step_id 确定性派生（含 run_id+plan_id+label+v1，见 R2-A1 方案 §2）
FROZEN_PLAN_STEP_NS = UUID("a1b2c3d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d")
# R2 收敛（rca_engine.RcaResult → contracts.RcaResult）：rca_id 确定性派生。
# 含 snapshot_version：同一 Run/资源 的 follow-up/re-score 多轮产生不同 rca_id（不可变可复算）。
FROZEN_RCA_NS = UUID("0f1c2a3e-4b5d-4c6e-8f7a-9b0c1d2e3f4a")


def plan_step_id(run_id: Any, plan_id: Any, label: str) -> UUID:
    """Y2：step_id = UUIDv5(NS, 'v1\\0'+run_id+'\\0'+plan_id+'\\0'+label)。

    含 plan_id：同 Run 不同重规划不复用步骤身份；含 'v1\\0' 版本前缀为规则演进预留。
    """
    import uuid as _uuid

    return _uuid.uuid5(
        FROZEN_PLAN_STEP_NS,
        f"v1\0{run_id}\0{plan_id}\0{label}",
    )


def rca_id(run_id: Any, resource_id: Any, snapshot_version: Any) -> UUID:
    """不可变 RCA 实体身份（R2 收敛 §7）。

    格式：UUIDv5(FROZEN_RCA_NS, canonical_run_id + '\\0' + resource_id + '\\0' + snapshot_version)。
    含 snapshot_version：区分同一 Run/资源 的 follow-up/re-score 多轮，不可变可复算。
    """
    import uuid as _uuid

    return _uuid.uuid5(
        FROZEN_RCA_NS,
        f"{normalize_uuid(run_id)}\0{str(resource_id or '')}\0{str(snapshot_version or '')}",
    )

# 非 UUID 的 legacy 字符串 evidence_id 处理策略：
#   "reject"          —— 未知 legacy ID 直接 fail-closed（ACL 只接受 UUID 或已解析实体）
#   "resolve_or_fail" —— 通过 fingerprint_index 解析验证实体存在，否则拒绝
EVIDENCE_ID_RESOLUTION = "resolve_or_fail"


def normalize_iso(value: Any) -> str:
    """时间 → ISO-8601 UTC 规范字符串（无小数秒、大写 Z）；None/缺失 → ""。

    关键（Bugbot B2/C2）：字符串时间也必须真正转换为 UTC，同一时刻的时区偏移/小数秒
    不同表示必须归一为同一 canonical 字符串，否则同一 Evidence 身份漂移。
    naive（无时区）时间必须拒绝——不能依赖机器本地时区，否则跨环境 Evidence ID 漂移。
    支持输入：
    - datetime：必须有 tzinfo（否则拒绝，fail-closed）
    - ISO 字符串：必须含时区（Z 或 ±HH:MM 偏移）；无时区（naive）拒绝
    - 其它无法解析/无时区的值 → 拒绝
    """
    if value is None:
        return ""
    if isinstance(value, datetime):
        if value.tzinfo is None:
            raise ValueError(
                f"naive datetime 无时区，拒绝（防跨环境身份漂移）: {value!r}；"
                f"必须显式带 tzinfo"
            )
        return value.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    s = str(value).strip()
    if s == "":
        return ""
    has_tz = s.endswith("Z") or ("+" in s[-6:] and ":" in s[-6:])
    if not has_tz:
        raise ValueError(
            f"时间字符串无时区（naive），拒绝（防跨环境身份漂移）: {value!r}"
        )
    try:
        parsed = datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        raise ValueError(f"非法时间表示，无法归一为 UTC: {value!r}")
    return parsed.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def normalize_uuid(value: Any) -> str:
    """UUID 字段规范化：可解析为 UUID → 小写规范形式；否则保留原值。

    说明：方案 B 要求 UUID 大小写规范化（同一 UUID 不同大小写 → 同一 canonical，
    防身份漂移）；但 MVP/隔离测试可能用非 UUID 标签（如 run-1/cluster-1），
    这些保留原值以便正确隔离，不强制拒绝（时间 naive 拒绝保持 fail-closed）。
    """
    if value is None or str(value).strip() == "":
        return ""
    try:
        return str(UUID(str(value))).lower()
    except (ValueError, TypeError):
        return str(value).strip()


def canonical_provenance_fields(
    *,
    source: str,
    query_id: Any,
    resource_id: Any,
    time_range_start: Any,
    time_range_end: Any,
    digest: str,
    tenant_id: Any,
    cluster_id: Any,
    run_id: Any,
) -> str:
    """固定顺序 + NUL 分隔的 canonical 拼接（见模块 docstring 规范）。"""
    parts = [
        str(source or ""),
        str(query_id or ""),
        str(resource_id or ""),
        normalize_iso(time_range_start),
        normalize_iso(time_range_end),
        str(digest or "").lower(),
        normalize_uuid(tenant_id),
        normalize_uuid(cluster_id),
        normalize_uuid(run_id),
    ]
    return "\x00".join(parts)


def provenance_fingerprint(canonical_fields: str) -> str:
    """SHA256(canonical_fields) 十六进制小写。"""
    return hashlib.sha256(canonical_fields.encode("utf-8")).hexdigest()


def evidence_id_from_fingerprint(fingerprint: str) -> UUID:
    """UUIDv5(FROZEN_NAMESPACE, fingerprint) —— 确定性 evidence_id。"""
    import uuid as _uuid

    return _uuid.uuid5(FROZEN_EVIDENCE_NAMESPACE, fingerprint)


def is_uuid(value: Any) -> bool:
    try:
        UUID(str(value))
        return True
    except (ValueError, TypeError):
        return False


def resolve_evidence_id(
    evidence_id: Any,
    *,
    fingerprint_index: Optional[Dict[str, str]] = None,
    existing_ids: Optional[Any] = None,
) -> UUID:
    """把 evidence 引用解析为权威 UUID evidence_id（阻断项 4 + Bugbot B3）。

    规则（fail-closed，防悬空引用）：
    - 已是 UUID：仅当能验证该实体存在（existing_ids 提供且包含该 UUID，或
      EVIDENCE_ID_RESOLUTION=="reject" 时要求必须存在）才返回；否则拒绝。
      若未提供 existing_ids（unknown 集合），则拒绝——不能假设任意 UUID 都对应实体。
    - 非 UUID 字符串：经 fingerprint_index（fingerprint → evidence_id UUID）解析，
      且该 evidence_id 必须在 existing_ids 中存在；否则拒绝。
    - None / 空 → fail-closed。
    """
    if evidence_id is None or str(evidence_id).strip() == "":
        raise ValueError("evidence 引用为空，拒绝")

    # 已存在实体 UUID 集合（大写的 UUID → 规范化小写）
    existing: Any = None
    if existing_ids is not None:
        existing = {str(u).lower() for u in existing_ids}

    if is_uuid(evidence_id):
        u = UUID(str(evidence_id))
        if existing is not None and str(u) in existing:
            return u
        if existing is not None:
            raise ValueError(
                f"evidence 引用 {evidence_id!r} 是 UUID 但对应实体不存在，拒绝（防悬空引用）"
            )
        raise ValueError(
            f"evidence 引用 {evidence_id!r} 是 UUID 但未提供实体存在集合，拒绝（防悬空引用）"
        )

    # 非 UUID：必须通过 fingerprint_index 解析
    if EVIDENCE_ID_RESOLUTION == "resolve_or_fail" and fingerprint_index:
        target = fingerprint_index.get(str(evidence_id))
        if target and is_uuid(target):
            u = UUID(str(target))
            if existing is not None and str(u) in existing:
                return u
            if existing is not None:
                raise ValueError(
                    f"evidence 引用 {evidence_id!r} 解析到 {u} 但实体不存在，拒绝（防悬空引用）"
                )
            raise ValueError(
                f"evidence 引用 {evidence_id!r} 解析到 {u} 但未提供实体存在集合，拒绝"
            )
        raise ValueError(
            f"evidence 引用 {evidence_id!r} 不是 UUID 且无法在 fingerprint_index 解析到实体，"
            f"拒绝（防悬空引用）"
        )
    raise ValueError(
        f"evidence 引用 {evidence_id!r} 不是 UUID 且未配置实体解析，拒绝（防悬空引用）"
    )


def fingerprint_from_evidence(evidence: Any) -> str:
    """从 evidence 对象（平行或权威）生成规范 fingerprint（供黄金向量验证）。"""
    return provenance_fingerprint(
        canonical_provenance_fields(
            source=evidence.source,
            query_id=getattr(evidence, "query_id", ""),
            resource_id=getattr(evidence, "resource_id", ""),
            time_range_start=getattr(evidence, "time_range_start", None),
            time_range_end=getattr(evidence, "time_range_end", None),
            digest=getattr(evidence, "raw_digest_sha256", ""),
            tenant_id=evidence.tenant_id,
            cluster_id=evidence.cluster_id,
            run_id=evidence.run_id,
        )
    )


# 黄金测试向量（跨语言共享，见 docs/contracts/bundle/v2/conformance-vectors.json）
GOLDEN_VECTORS = [
    {
        "source": "VM",
        "query_id": "qry-1",
        "resource_id": "svc:orders",
        "time_range_start": "2026-08-19T09:00:00Z",
        "time_range_end": "2026-08-19T10:00:00Z",
        "digest": "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
        "tenant_id": "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
        "cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
        "run_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    },
    {
        "source": "VLogs",
        "query_id": "",
        "resource_id": "",
        "time_range_start": None,
        "time_range_end": None,
        "digest": "",
        "tenant_id": "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
        "cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
        "run_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    },
    {
        "source": "k8sgpt",
        "query_id": "qry-3",
        "resource_id": "pod:checkout-7f8d",
        "time_range_start": "2026-08-19T09:00:00Z",
        "time_range_end": "2026-08-19T10:00:00Z",
        "digest": "f0e0d0c0b0a090807060504030201000f0e0d0c0b0a090807060504030201000",
        "tenant_id": "7ed01afc-cc79-4ecd-8767-a2befa6168ad",
        "cluster_id": "91771a6e-9c2d-11f1-8271-bea176fe9f9f",
        "run_id": "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
    },
]


def golden_vector(v: Dict[str, Any]) -> str:
    """根据黄金向量字典计算 fingerprint（供测试断言）。"""
    return provenance_fingerprint(
        canonical_provenance_fields(
            source=v["source"],
            query_id=v.get("query_id", ""),
            resource_id=v.get("resource_id", ""),
            time_range_start=v.get("time_range_start"),
            time_range_end=v.get("time_range_end"),
            digest=v.get("digest", ""),
            tenant_id=v.get("tenant_id", ""),
            cluster_id=v.get("cluster_id", ""),
            run_id=v.get("run_id", ""),
        )
    )
