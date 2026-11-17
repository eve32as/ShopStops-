export const workspaces = [
  { id: 'overview', label: 'Overview', title: 'The right tool for the next step', description: 'Internal operations, not a customer or worker app. Choose a focused workspace below.' },
  { id: 'refund-desk', label: 'Refund desk', title: 'Resolve a checkout', description: 'Request a full remaining refund. Stripe confirmation and reconciliation happen separately.' },
  { id: 'finance-planning', label: 'Finance planning', title: 'Plan the economics', description: 'Compare explicit assumptions and record manual costs. Estimates are not actual income or approved worker pay.' },
  { id: 'shopper-orders', label: 'Shopper orders', title: 'Guide a supervised completion', description: 'Assign an order, verify evidence, then review the record. No payments are sent here.' },
] as const;
export type Workspace = typeof workspaces[number]['id'];
export function workspaceFromHash(hash: string): Workspace {
  return workspaces.find(item => `#${item.id}` === hash)?.id ?? 'overview';
}