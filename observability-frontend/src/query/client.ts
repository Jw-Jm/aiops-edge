import { QueryClient } from '@tanstack/react-query'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

/**
 * Scope is an authorization boundary, not merely a filter. Drop in-flight and
 * cached reads before committing a newly confirmed server scope so an old
 * cluster projection cannot flash after a switch.
 */
export async function resetScopeQueries(): Promise<void> {
  await queryClient.cancelQueries()
  queryClient.removeQueries()
}
