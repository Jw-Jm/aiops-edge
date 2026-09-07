function telemetryBlocker({ marker, reason, traceCount = 0, ...details }) {
  return {
    blocker_code: 'TELEMETRY_PRECONDITION_UNMET',
    marker,
    reason,
    trace_count: traceCount,
    ...details,
  }
}

function traceHasParentChild(trace) {
  const spans = Array.isArray(trace?.spans) ? trace.spans : Array.isArray(trace?.span_list) ? trace.span_list : []
  if (spans.length < 2) return false
  const ids = new Set(spans.map((span) => span.span_id || span.spanId).filter(Boolean))
  return spans.some((span) => {
    const parent = span.parent_span_id || span.parentSpanId
    return Boolean(parent && ids.has(parent))
  })
}

function isFreshTelemetry(value, marker, now = Date.now(), maxAgeMs = 24 * 60 * 60 * 1000) {
  if (!value || value.marker !== marker || !value.timestamp) return false
  const timestamp = Date.parse(value.timestamp)
  return Number.isFinite(timestamp) && timestamp <= now && now - timestamp <= maxAgeMs
}

function collectTelemetryEvidence({ marker, stats, services, traces, logs, serviceMap }) {
  const serialized = JSON.stringify({ stats, services, traces, logs, serviceMap })
  return {
    marker,
    trend_count: Array.isArray(stats?.trend) ? stats.trend.length : 0,
    services_found: ['payments', 'orders'].filter((name) => serialized.includes(name)),
    trace_marker_found: serialized.includes(marker),
    log_marker_found: serialized.includes(marker),
    dependency_found: /payments.*orders|orders.*payments/.test(serialized),
    collected_at: new Date().toISOString(),
  }
}

module.exports = { collectTelemetryEvidence, isFreshTelemetry, telemetryBlocker, traceHasParentChild }
