"""异常检测引擎 — 3 算法融合投票 + 多维指纹提取"""
import statistics
from collections import deque
from dataclasses import dataclass, field


@dataclass
class AnomalyResult:
    service: str
    metric: str
    current_value: float
    expected_range: tuple   # (lower, upper)
    method: str             # "3sigma" | "iqr" | "rate_change"
    severity: str           # "warning" | "critical"
    score: float            # 偏离程度 0-1


@dataclass
class AnomalyFingerprint:
    service: str
    binary_vector: list[int]
    abnormal_dimensions: list[str]
    normal_dimensions: list[str]
    severity_scores: dict[str, float]
    pattern_signature: str


class AnomalyDetector:
    """滑动窗口多算法异常检测器"""

    # history 键上限：按 (service:metric) 无限增长会 OOM，超过时丢弃最久未更新的键。
    MAX_KEYS = 20000

    DIMENSIONS = [
        # 资源层
        "cpu_usage", "memory_usage", "fd_count", "tcp_connections",
        "disk_iops_read", "disk_iops_write", "network_bytes_in", "network_bytes_out",
        # 运行时层
        "gc_pause_ms", "thread_count", "heap_used", "goroutines",
        # 应用层
        "p99_latency_ms", "error_rate", "request_rate",
        "db_connections_active", "db_connections_idle",
        # 依赖层
        "redis_latency_ms", "redis_errors",
        "dns_query_rate", "dns_failure_rate",
        # 业务层
        "circuit_breaker_open", "retry_rate", "timeout_rate",
    ]

    def __init__(self, window_size: int = 60):
        self.window_size = window_size
        self.history: dict[str, deque] = {}  # key → 最近 N 个数据点

    def _key(self, service: str, metric: str) -> str:
        return f"{service}:{metric}"

    def feed(self, service: str, metric: str, value: float):
        """持续喂数据，维护滑动窗口"""
        key = self._key(service, metric)
        if key not in self.history:
            # 容量保护：超过 MAX_KEYS 时丢弃最久未更新的键（dict 保持插入顺序，首项最旧）
            if self.MAX_KEYS > 0 and len(self.history) >= self.MAX_KEYS:
                try:
                    self.history.pop(next(iter(self.history)))
                except Exception:
                    pass
            self.history[key] = deque(maxlen=self.window_size)
        self.history[key].append(value)

    def detect(self, service: str, metric: str, current: float) -> list[AnomalyResult]:
        """三算法检测，返回触发的异常列表"""
        key = self._key(service, metric)
        values = list(self.history.get(key, []))
        if len(values) < 10:
            return []  # 数据不足

        # 喂入当前值
        self.feed(service, metric, current)
        values = list(self.history.get(key, []))

        results = []
        r = self._detect_3sigma(values, current, service, metric)
        if r: results.append(r)
        r = self._detect_iqr(values, current, service, metric)
        if r: results.append(r)
        r = self._detect_rate_change(values, current, service, metric)
        if r: results.append(r)
        return results

    def vote(self, results: list[AnomalyResult]) -> AnomalyResult | None:
        """>= 2/3 算法触发 → 确认异常，取最高分"""
        if len(results) >= 2:
            return max(results, key=lambda r: r.score)
        return None

    # ── 算法 1: 3-Sigma ──
    def _detect_3sigma(self, values: list, current: float,
                       service: str, metric: str) -> AnomalyResult | None:
        if len(values) < 20:
            return None
        historical = values[:-1]  # 排除当前值
        mu = statistics.mean(historical)
        sigma = statistics.stdev(historical) if len(historical) > 2 else 0
        if sigma == 0:
            return None
        z = abs(current - mu) / sigma
        if z > 3:
            return AnomalyResult(service=service, metric=metric,
                current_value=current, expected_range=(mu - 3*sigma, mu + 3*sigma),
                method="3sigma", severity="critical" if z > 5 else "warning",
                score=min(z / 10, 1.0))
        return None

    # ── 算法 2: IQR ──
    def _detect_iqr(self, values: list, current: float,
                    service: str, metric: str) -> AnomalyResult | None:
        if len(values) < 20:
            return None
        sorted_vals = sorted(values[:-1])
        n = len(sorted_vals)
        q1 = sorted_vals[n // 4]
        q3 = sorted_vals[3 * n // 4]
        iqr = q3 - q1
        if iqr == 0:
            return None
        upper = q3 + 1.5 * iqr
        lower = q1 - 1.5 * iqr
        if current > upper or current < lower:
            deviation = max(abs(current - upper), abs(current - lower))
            score = min(deviation / max(iqr, 1) / 5, 1.0)
            return AnomalyResult(service=service, metric=metric,
                current_value=current, expected_range=(lower, upper),
                method="iqr", severity="critical" if deviation > 3 * iqr else "warning",
                score=score)
        return None

    # ── 算法 3: 环比突变 ──
    def _detect_rate_change(self, values: list, current: float,
                            service: str, metric: str) -> AnomalyResult | None:
        if len(values) < 10:
            return None
        recent = values[-5:]  # 最近 5 个点
        prev = values[-10:-5]  # 之前 5 个点
        recent_avg = statistics.mean(recent) if recent else 1
        prev_avg = statistics.mean(prev) if prev else 1
        if prev_avg == 0:
            prev_avg = recent_avg * 0.1 or 1
        ratio = recent_avg / prev_avg
        if ratio > 2.0 or ratio < 0.5:
            return AnomalyResult(service=service, metric=metric,
                current_value=current,
                expected_range=(prev_avg * 0.5, prev_avg * 2.0),
                method="rate_change",
                severity="critical" if ratio > 5 or ratio < 0.2 else "warning",
                score=min(abs(ratio - 1), 5) / 5)
        return None

    # ── 多维指纹提取 (未知故障用) ──
    def extract_fingerprint(self, service: str,
                            snapshot: dict[str, float],
                            baselines: dict[str, tuple]) -> AnomalyFingerprint:
        """多维指标异常指纹: 对比当前值 vs 基线 P99"""
        abnormal_dims = []
        normal_dims = []
        severity = {}
        binary = []

        for dim in self.DIMENSIONS:
            current = snapshot.get(dim)
            bl = baselines.get(dim, (0, 0, 0))
            if current is None:
                continue
            p99 = bl[2] if len(bl) > 2 else 0
            if p99 == 0:
                is_abnormal = current > 0
            else:
                is_abnormal = abs(current - p99) / p99 > 3.0

            binary.append(1 if is_abnormal else 0)
            severity[dim] = min(abs(current - p99) / max(p99, 1), 10.0)
            (abnormal_dims if is_abnormal else normal_dims).append(dim)

        # 构建人类可读签名
        interesting = abnormal_dims[:5] + [
            f"NORMAL:{d}" for d in normal_dims
            if d in ("error_rate", "circuit_breaker_open")
        ]
        pattern_sig = " + ".join(interesting[:8])

        return AnomalyFingerprint(
            service=service,
            binary_vector=binary,
            abnormal_dimensions=abnormal_dims,
            normal_dimensions=normal_dims,
            severity_scores=severity,
            pattern_signature=pattern_sig,
        )


# 全局单例
detector = AnomalyDetector(window_size=60)
