"""Pydantic models for FastAPI"""
from typing import Optional
from pydantic import BaseModel


class ChatRequest(BaseModel):
    intent: str = "diagnosis"
    service: str = ""
    message: str = ""
    stream: bool = True
    session_id: Optional[str] = None
    dual_agent: bool = False   # 批3: 双层 Agent 开关（默认关闭，零回归）
    # 需求2/3: 审批内嵌 aichat。确认后继续分析时，前端携 script 与执行结果回传，
    # 后端据此进行下一轮深入分析（复用同一 thread_id，直至输出最终报告）
    script: str = ""           # 待确认/已确认执行的处置脚本
    approved: Optional[bool] = None  # None=未知, True=确认执行, False=驳回
    exec_result: str = ""      # 上一轮处置脚本的执行结果（作为下一轮分析上下文）


class ShellCheckRequest(BaseModel):
    command: str = ""


class MCPCallRequest(BaseModel):
    name: str = ""
    args: dict = {}


class TaskCreateRequest(BaseModel):
    source: str = "manual"  # alert | log_anomaly | manual | inspection
    service: str = ""
    context: str = ""


class AlertRCARequest(BaseModel):
    """告警事件 → RCA 假设引擎联动请求"""
    service: str = "kubernetes"
    rule_id: str = ""
    rule_name: str = ""
    severity: str = "warning"
    message: str = ""
    count: int = 0
    first_timestamp: str = ""
    last_timestamp: str = ""
    namespace: str = ""  # 告警对象所在的真实命名空间，kubectl 查询用真实值（不再硬编码 observability）
    object: str = ""  # 告警对象（如 Pod 名列表），用于 RCA 针对性过滤集群状态


class WebhookPayload(BaseModel):
    """统一 webhook 入口：vmalert / VL / CronJob 三种触发源"""
    source: str = "alert"      # alert | log_anomaly | inspection
    service: str = ""
    summary: str = ""
    context: str = ""
    alert_name: str = ""
    severity: str = "warning"  # info | warning | critical
    raw: Optional[dict] = None


class CaseSearchRequest(BaseModel):
    query: str
    limit: int = 5


class ScanAnomaliesRequest(BaseModel):
    services: list[str] = []
    metrics: list[str] = ["error_rate", "p99_latency", "request_rate"]
