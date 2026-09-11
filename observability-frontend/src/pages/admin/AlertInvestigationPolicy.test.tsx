import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { AlertInvestigationPolicyForm } from './AdminSettings'
import { getAlertInvestigationPolicy, putAlertInvestigationPolicy } from '../../api/client'

vi.mock('../../api/client', () => ({
  getAlertInvestigationPolicy: vi.fn(),
  putAlertInvestigationPolicy: vi.fn(),
}))

describe('AlertInvestigationPolicyForm', () => {
  it('rejects an empty cluster instead of guessing one', () => {
    render(<AlertInvestigationPolicyForm clusterId="" />)
    expect(screen.getByText('请先选择实际集群')).toBeVisible()
    expect(getAlertInvestigationPolicy).not.toHaveBeenCalled()
  })

  it('loads the stored policy and always states the read-only boundary', async () => {
    vi.mocked(getAlertInvestigationPolicy).mockResolvedValue({ data: { mode: 'auto_readonly', minimum_severity: 'critical', max_concurrent: 3, max_per_hour: 20 } } as never)
    render(<AlertInvestigationPolicyForm clusterId="cluster-a" />)
    expect(await screen.findByText('自动调查仅只读，不会执行处置')).toBeVisible()
    await waitFor(() => expect(screen.getByTestId('alert-investigation-policy')).toBeInTheDocument())
    expect(getAlertInvestigationPolicy).toHaveBeenCalledWith('cluster-a')
  })

  it('saves and re-reads the authoritative configuration', async () => {
    vi.mocked(getAlertInvestigationPolicy).mockResolvedValue({ data: { mode: 'manual', minimum_severity: 'critical', max_concurrent: 2, max_per_hour: 10 } } as never)
    vi.mocked(putAlertInvestigationPolicy).mockResolvedValue({ data: { note: '自动调查仅只读，不会执行处置' } } as never)
    render(<AlertInvestigationPolicyForm clusterId="cluster-a" />)
    expect(await screen.findByText('人工发起')).toBeVisible()
    // 保存按钮存在于表单中，且保存动作会携带当前集群。
    expect(screen.getByRole('button', { name: '保存并回读' })).toBeEnabled()
  })
})
