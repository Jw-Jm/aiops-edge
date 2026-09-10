import React, { useEffect, useState } from 'react'
import { useScopeStore } from '../../store/scopeStore'
import { getResourceDetail, type ResourceDetailResponse } from '../../api/resources'
import DataState from '../../components/display/DataState'
import ResourceIdentity from './ResourceIdentity'
import MainFailureChain from './MainFailureChain'
import ResourceDirectory from './ResourceDirectory'
import VmDependencyPanel from './VmDependencyPanel'
import { projectVmDependencies } from './resourceDetailModel'

const ResourceCenter: React.FC<{ children: React.ReactNode; clusterName?: string }> = ({ children, clusterName }) => {
  const active = useScopeStore((state) => state.active ?? { tenantId: '', clusterId: '', timeRange: { mode: 'relative' as const, minutes: 60 } })
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? state.active.clusterId ?? '')
  const setResource = useScopeStore((state) => state.setResource)
  const [detail, setDetail] = useState<ResourceDetailResponse | undefined>()
  const [detailError, setDetailError] = useState(false)
  useEffect(() => {
    if (!active.resource) { setDetail(undefined); return }
    const controller = new AbortController()
    setDetailError(false)
    void getResourceDetail(active.resource.uid, controller.signal).then(setDetail).catch(() => { if (!controller.signal.aborted) setDetailError(true) })
    return () => controller.abort()
  }, [active.resource?.uid])
  if (!active.resource) return <div className="resource-center"><div className="resource-center__panorama">{children}</div></div>
  return <div className="resource-layout resource-center">
    <aside className="context-panel"><ResourceDirectory clusterId={active.clusterId || activeClusterId} selected={active.resource} onSelect={setResource} /></aside>
    <main><ResourceIdentity resource={active.resource} clusterName={clusterName || activeClusterId} detail={detail} />{detailError && <DataState kind="error" compact title="资源详情读取失败" />}{detail && (active.resource.type === 'vm' || active.resource.type === 'vmi') && <VmDependencyPanel dependencies={projectVmDependencies(detail.data.attributes)} />}<div style={{ marginTop: 16 }}>{children}</div></main>
    <aside className="context-panel"><MainFailureChain center={active.resource} /></aside>
  </div>
}

export default ResourceCenter
