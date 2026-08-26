/**
 * Extract an explicit backend availability error from a cluster-detail response.
 * An empty list is a valid successful response and must remain distinguishable
 * from a response that contains an error reason.
 */
export function clusterDetailError(data: unknown): string {
  if (!data || typeof data !== 'object') return ''
  const error = (data as { error?: unknown }).error
  return typeof error === 'string' ? error.trim() : ''
}
