import React from 'react'
import type { AssistantConversationScope } from '../../api/assistant'

export default function AssistantContextPanel({ scope }: { scope: AssistantConversationScope }) {
  return <aside className="assistant-context-panel" aria-label="会话 Scope">
    <div className="assistant-context-panel__title">会话 Scope</div>
    <span className="assistant-scope-lock">已冻结</span>
    <dl>
      <div><dt>集群</dt><dd>{scope.clusterId}</dd></div>
      {scope.resourceUid && <div><dt>资源 UID</dt><dd>{scope.resourceUid}</dd></div>}
      <div><dt>时间窗口</dt><dd>{scope.from} → {scope.to}</dd></div>
      <div><dt>知识范围</dt><dd>平台通用 + 当前集群</dd></div>
    </dl>
  </aside>
}
