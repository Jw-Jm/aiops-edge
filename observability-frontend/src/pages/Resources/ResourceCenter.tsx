import React from 'react'
import { useScopeStore } from '../../store/scopeStore'
import ResourceIdentity from './ResourceIdentity'
import MainFailureChain from './MainFailureChain'

const ResourceCenter: React.FC<{ children: React.ReactNode; clusterName?: string }> = ({ children, clusterName }) => {
  const active = useScopeStore((state) => state.active ?? { tenantId: '', clusterId: '', timeRange: { mode: 'relative' as const, minutes: 60 } })
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  return <><ResourceIdentity resource={active.resource} clusterName={clusterName || activeClusterId} /><MainFailureChain center={active.resource} /><div style={{ marginTop: 16 }}>{children}</div></>
}

export default ResourceCenter
