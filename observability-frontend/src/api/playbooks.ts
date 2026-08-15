import api from './client'

// ===== 运维 Playbook（内置运维知识库，Task E4）=====
export interface PlaybookEntry {
  path: string
  title?: string
  category?: string
  source?: string
  tags?: string[]
  score?: number
  preview?: string
}

export interface PlaybookDoc {
  path: string
  content: string
}

// 列表：无 q 返回全量 { playbooks }；带 q 走向量检索返回 { items }
export const getPlaybooks = (params?: { category?: string; q?: string }) =>
  api.get<{ playbooks?: PlaybookEntry[]; items?: PlaybookEntry[] }>('/ai/knowledge/playbooks', { params })

// 获取单篇原文（path 需 encodeURIComponent）
export const getPlaybookContent = (path: string) =>
  api.get<PlaybookDoc>(`/ai/knowledge/playbooks/${encodeURIComponent(path)}`)
