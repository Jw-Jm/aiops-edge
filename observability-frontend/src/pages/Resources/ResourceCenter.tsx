import React from 'react'
import { useScopeStore } from '../../store/scopeStore'
import ResourceIdentity from './ResourceIdentity'
import MainFailureChain from './MainFailureChain'

const ResourceCenter: React.FC<{ children: React.ReactNode; clusterName?: string }> = ({ children, clusterName }) => {
  const context = useScopeStore((state) => state.context)
  const activeClusterId = useScopeStore((state) => state.authScope?.activeClusterId ?? '')
  return <><ResourceIdentity resource={context.resource} clusterName={clusterName || activeClusterId} /><MainFailureChain center={context.resource} /><div style={{ marginTop: 16 }}>{children}</div></>
}

export default ResourceCenter
