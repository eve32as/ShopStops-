export function recordState(busy: boolean, error: string, loaded: boolean, count: number) {
  if (busy) return { kind: 'busy', title: 'Contacting gateway…', detail: 'Keep this workspace open. Do not submit a duplicate request.' } as const
  if (error) return { kind: 'error', title: 'Request not completed', detail: error } as const
  if (!loaded) return { kind: 'idle', title: 'Records have not been loaded', detail: 'Refresh to request current records. No requests are sent just by opening this workspace.' } as const
  if (count === 0) return { kind: 'empty', title: 'No records returned on this page', detail: 'Try the previous page or refresh. This does not establish payment or eligibility status.' } as const
  return { kind: 'ready', title: `${count} record(s) on this page`, detail: 'These are completion records, not proof of payment.' } as const
}