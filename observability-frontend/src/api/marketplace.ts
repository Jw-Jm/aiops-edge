import api from './client'

// ===== Skill Marketplace（Skill 市场，Task D5）=====
export type SourceType = 'local' | 'tarball' | 'git'
export type SignatureState = 'verified' | 'unsigned' | 'failed'

export interface InstalledPack {
  pack_id: string
  source: string
  signature_state: SignatureState
  installed_at?: string
  [key: string]: unknown
}

export interface InstallResult {
  pack_id: string
  signature_state: SignatureState
  skills: string[]
}

// 安装 pack（source：本地目录路径 / tarball 路径 / git URL）
export const installMarketplacePack = (source: string) =>
  api.post<InstallResult>('/ai/marketplace/install', { source })

// 已安装 pack 列表
export const listInstalledPacks = () =>
  api.get<{ installed: InstalledPack[] }>('/ai/marketplace/installed')

// 卸载 pack
export const uninstallMarketplacePack = (packId: string) =>
  api.delete(`/ai/marketplace/installed/${encodeURIComponent(packId)}`)
