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
