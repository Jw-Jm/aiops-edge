import React, { useEffect, useState } from 'react'
import { Drawer, Spin, Table, Tag, Descriptions, Typography } from 'antd'
import { listVms, getVm, type VmItem } from '../../api/client'
import { useUIStore } from '../../store/uiStore'
import { PageHeader, Breadcrumb, Empty } from '../../components/ui/PageKit'
import ErrorState from '../../components/ErrorState'

const { Text } = Typography

// 状态 → Tag 颜色：Running=绿 Stopped=灰 Failed=红 其他=橙
function vmTone(s?: string) {
  if (s === 'Running' || s === 'running') return 'green'
  if (s === 'Stopped' || s === 'stopped') return 'default'
  if (s === 'Failed' || s === 'failed' || s === 'Error' || s === 'error') return 'red'
  return 'orange'
}

const VirtualMachines: React.FC = () => {
  const currentClusterId = useUIStore((s) => s.currentClusterId)
  const [rows, setRows] = useState<VmItem[]>([])
  const [loading, setLoading] = useState(true)
  // KubeVirt 未安装标记：接口返回 kubevirt_not_installed=true 时显示空态引导
  const [kubevirtMissing, setKubevirtMissing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [detail, setDetail] = useState<any>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerLoading, setDrawerLoading] = useState(false)
  const [detailError, setDetailError] = useState<string | null>(null)

  const load = async () => {
    setLoading(true)
    setError(null)
    try {
      const r = await listVms({ cluster_id: currentClusterId === 'all' ? undefined : currentClusterId })
        const d = r.data
        if (d && (d.kubevirt_not_installed === true || d.kubevirt_installed === false)) {
          setKubevirtMissing(true)
          setRows([])
        } else {
          setKubevirtMissing(false)
          const list = Array.isArray(d) ? d : (d?.vms ?? d?.items ?? d?.data ?? [])
          setRows(Array.isArray(list) ? list : [])
        }
    } catch (e: any) {
      setRows([])
      setKubevirtMissing(false)
      setError(e?.response?.data?.error || e?.message || '虚拟机数据加载失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [currentClusterId])

  const openDetail = async (vm: VmItem) => {
    setDrawerOpen(true)
    setDrawerLoading(true)
    setDetailError(null)
    setDetail(null)
    try {
      const r = await getVm(vm.namespace, vm.name)
      const d = r.data
      setDetail(d?.vm ?? d?.data ?? d ?? {})
    } catch (e: any) {
      setDetail({})
      setDetailError(e?.response?.data?.error || e?.message || '虚拟机详情加载失败')
    } finally {
      setDrawerLoading(false)
    }
  }

  const cols = [
    // B6 修复：移除名称列 onClick，仅保留 onRow 点击打开详情（避免双击触发两次请求）
    { title: '名称', dataIndex: 'name', key: 'name', render: (v: string) => <span style={{ color: 'var(--primary)', fontWeight: 500 }}>{v}</span> },
    { title: '命名空间', dataIndex: 'namespace', key: 'namespace', width: 160 },
    { title: '状态', dataIndex: 'status', key: 'status', width: 110, render: (s?: string) => <Tag color={vmTone(s)}>{s || '-'}</Tag> },
    { title: '所在节点', dataIndex: 'node', key: 'node', width: 160, render: (v?: string) => <span style={{ fontSize: 12 }}>{v || '-'}</span> },
    { title: '规格', key: 'spec', width: 200, render: (_: any, r: VmItem) => <span style={{ fontSize: 12 }}>{[r.cpu, r.memory].filter(Boolean).join(' · ') || '-'}</span> },
  ]

  const detailEvents = detail?.events || detail?.vm?.events || []

  return (
    <div>
      <Breadcrumb items={[{ t: '可观测' }, { t: '虚拟机' }]} />
      <PageHeader title="虚拟机" desc="KubeVirt 虚拟机列表与运行状态 · 点击行查看详情" />

      <div className="card" style={{ padding: 0 }}>
        {error ? <ErrorState message={error} onRetry={load} /> : <Table rowKey={(r) => `${r.namespace}-${r.name}`} loading={loading} columns={cols} dataSource={rows}
          size="middle" pagination={{ pageSize: 20 }} scroll={{ x: 800 }}
          onRow={(r) => ({ onClick: () => openDetail(r), style: { cursor: 'pointer' } })}
          locale={{ emptyText: kubevirtMissing
            ? <Empty text="KubeVirt 未安装" hint="请先在集群部署 KubeVirt 后使用虚拟机管理" />
            : <Empty text="暂无虚拟机" hint="集群中未发现 KubeVirt 虚拟机" /> }} />}
      </div>

      <Drawer width={620} open={drawerOpen} onClose={() => setDrawerOpen(false)} destroyOnHidden
        title={`虚拟机 ${detail?.name || ''}`}
        styles={{ body: { padding: 16, background: 'var(--surface-1)' } }}>
        {drawerLoading ? <div style={{ textAlign: 'center', padding: 60 }}><Spin /></div> : detailError ? <ErrorState message={detailError} /> : (
          <div>
            <Descriptions size="small" column={1} style={{ marginBottom: 16 }}>
              <Descriptions.Item label="状态"><Tag color={vmTone(detail?.status)}>{detail?.status || '-'}</Tag></Descriptions.Item>
              <Descriptions.Item label="命名空间">{detail?.namespace || '-'}</Descriptions.Item>
              <Descriptions.Item label="所在节点">{detail?.node || '-'}</Descriptions.Item>
              <Descriptions.Item label="IP">{detail?.ip || detail?.interfaces?.map((i: any) => i?.ipAddress).filter(Boolean).join(', ') || '-'}</Descriptions.Item>
              <Descriptions.Item label="CPU">{detail?.cpu || '-'}</Descriptions.Item>
              <Descriptions.Item label="内存">{detail?.memory || '-'}</Descriptions.Item>
            </Descriptions>
            <Text strong style={{ fontSize: 13 }}>事件 ({Array.isArray(detailEvents) ? detailEvents.length : 0})</Text>
            <div style={{ marginTop: 8 }}>
              {Array.isArray(detailEvents) && detailEvents.length > 0 ? (
                detailEvents.map((e: any, i: number) => (
                  <div key={i} style={{ padding: '8px 10px', marginBottom: 6, borderRadius: 6, background: 'var(--surface-2)', border: '1px solid var(--border)', fontSize: 12 }}>
                    <div style={{ color: 'var(--text-muted)', marginBottom: 2 }}>
                      {e?.lastTimestamp || e?.time || ''} {e?.type ? <Tag style={{ marginLeft: 4 }}>{e.type}</Tag> : null}
                    </div>
                    <div>{e?.message || e?.reason || ''}</div>
                  </div>
                ))
              ) : (
                <div style={{ color: 'var(--text-muted)', fontSize: 12, padding: '8px 0' }}>暂无事件</div>
              )}
            </div>
          </div>
        )}
      </Drawer>
    </div>
  )
}

export default VirtualMachines
