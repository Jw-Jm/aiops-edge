import { api } from './client'

/**
 * 报告合同（设计规范 §6.7）
 *
 * 只保留两类主入口：巡检报告、AI 运维报告。页面、导出和 API 必须使用
 * 同一聚合与单位；报告绑定 scope、时间窗、查询合同与版本。
 */

export interface ReportRow {
  id: number
  taskId: string
  reportType: string
  verdict: string
  riskScore: number | null
  summary: string
  content: string
  serviceName: string
  createdAt: string
  /** 类型化对象身份：仅在服务端提供时展示，不用"服务"泛称所有对象 */
  resourceUid?: string
  resourceType?: string
  resourceName?: string
  resourceNamespace?: string
  sourceRunId?: string
}

export interface ReportInspectionSection {
  key: string
  title: string
  status: string
  facts: Record<string, unknown>[]
  gaps: string[]
  source: string
  query_window: Record<string, string>
}

export interface InspectionDocument {
  scope: Record<string, string>
  time_window: Record<string, string>
  query_contract: string
  versions: Record<string, string>
  sections: ReportInspectionSection[]
  quality: string
  warnings: string[]
}

export interface GeneratedInspectionReport {
  id: number
  verdict: string
  summary: string
  content: string
  document: InspectionDocument
  generatedAt: string
}

function toRow(raw: Record<string, unknown>): ReportRow {
  return {
    id: Number(raw.id ?? 0),
    taskId: String(raw.task_id ?? ''),
    reportType: String(raw.report_type ?? ''),
    verdict: String(raw.verdict ?? ''),
    riskScore: typeof raw.risk_score === 'number' ? raw.risk_score : null,
    summary: String(raw.summary ?? ''),
    content: String(raw.content ?? ''),
    serviceName: String(raw.service_name ?? ''),
    createdAt: String(raw.created_at ?? ''),
    ...(raw.resource_uid ? { resourceUid: String(raw.resource_uid) } : {}),
    ...(raw.resource_type ? { resourceType: String(raw.resource_type) } : {}),
    ...(raw.resource_name ? { resourceName: String(raw.resource_name) } : {}),
    ...(raw.namespace ? { resourceNamespace: String(raw.namespace) } : {}),
    ...(raw.source_run_id ? { sourceRunId: String(raw.source_run_id) } : {}),
  }
}

/** 巡检报告模板变体只作为类型/筛选，不扩展一级导航。 */
export const INSPECTION_TEMPLATES = [
  { id: 'standard', label: '标准巡检', desc: '告警、计算、Kubernetes、网络、存储、数据质量' },
  { id: 'capacity', label: '容量巡检', desc: '集群口径 CPU/内存、热点节点与容量风险' },
  { id: 'kubernetes', label: 'Kubernetes 巡检', desc: 'Pod 阶段、就绪与容器重启' },
] as const

export type InspectionTemplateId = (typeof INSPECTION_TEMPLATES)[number]['id']

export async function listReports(params: { clusterId: string; limit?: number }, signal?: AbortSignal): Promise<ReportRow[]> {
  const res = await api.get<{ reports?: Record<string, unknown>[] } | Record<string, unknown>[]>('/ops/reports', {
    params: { cluster_id: params.clusterId, limit: params.limit ?? 100 },
    signal,
  })
  const raw = Array.isArray(res.data) ? res.data : (res.data?.reports ?? [])
  return raw.map(toRow)
}

export async function generateInspectionReport(
  params: { clusterId: string; template: InspectionTemplateId; from?: string; to?: string },
  signal?: AbortSignal,
): Promise<GeneratedInspectionReport> {
  const res = await api.post<Record<string, unknown>>(
    '/ops/reports/inspection',
    { template: params.template, from: params.from, to: params.to },
    { params: { cluster_id: params.clusterId }, signal },
  )
  const d = res.data ?? {}
  return {
    id: Number(d.id ?? 0),
    verdict: String(d.verdict ?? ''),
    summary: String(d.summary ?? ''),
    content: String(d.content ?? ''),
    document: (d.document ?? { scope: {}, time_window: {}, query_contract: '', versions: {}, sections: [], quality: '', warnings: [] }) as InspectionDocument,
    generatedAt: String(d.generated_at ?? ''),
  }
}

export function reportDownloadUrl(clusterId: string, reportId: number): string {
  return `/api/v1/ops/reports/${encodeURIComponent(String(reportId))}/download?cluster_id=${encodeURIComponent(clusterId)}`
}

/** AI 运维报告生成：从已完成/终止的 Run 聚合真实事实（§6 报告，唯一聚合源头）。 */
export async function generateAIOperationsReport(params: {
  clusterId: string
  runId: string
}): Promise<{ id: number; verdict: string; evidenceCount: number; summary: string; content?: string }> {
  const res = await api.post<Record<string, unknown>>(
    '/ops/reports/ai-operations',
    { run_id: params.runId },
    { params: { cluster_id: params.clusterId } },
  )
  const d = res.data ?? {}
  return {
    id: Number(d.report_id ?? 0),
    verdict: String(d.verdict ?? 'Unknown'),
    evidenceCount: Number(d.evidence_count ?? 0),
    summary: String(d.summary ?? ''),
    content: d.content ? String(d.content) : undefined,
  }
}

export async function downloadReport(clusterId: string, reportId: number): Promise<Blob> {
  const res = await api.get(`/ops/reports/${encodeURIComponent(String(reportId))}/download`, {
    params: { cluster_id: clusterId },
    responseType: 'blob',
  })
  return res.data as Blob
}

/** 报告分类：巡检 / AI 运维；其余历史类型不进入两类主入口。 */
export function reportBucket(reportType: string): 'inspection' | 'ai' | 'other' {
  const t = reportType.toLowerCase()
  if (t.startsWith('inspection')) return 'inspection'
  if (t.startsWith('ai')) return 'ai'
  return 'other'
}
