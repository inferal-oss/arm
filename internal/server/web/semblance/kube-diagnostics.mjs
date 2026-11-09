// Fixed categories only: API/credential/helper diagnostic text is never product copy.
const notices = {
  kubernetes_forbidden: 'Access denied. The selected Kubernetes identity cannot read this inventory. Check its RBAC permissions, then refresh.',
  kubernetes_unauthorized: 'Credentials were not accepted. Check the selected Kubernetes identity, then refresh.',
  kubernetes_not_found: 'The selected Namespace or Node no longer exists. Refresh the selection.',
  kubernetes_timeout: 'The cluster took too long to respond. Refresh to try reading Nodes again.',
  kubernetes_cancelled: 'The inventory read was cancelled. Refresh before selecting a Node.',
  kubernetes_inventory_invalid: 'Could not use the Node inventory returned by Kubernetes. Refresh the selection.',
  kubernetes_identity_changed: 'The selected cluster or Namespace changed. Refresh before selecting a Node.'
};
export function kubernetesInventoryNotice(error, stage = 'nodes') {
  if (stage === 'contexts' && error?.code === 'kubernetes_inventory_invalid') return 'Could not use the Kubernetes contexts returned by the backend. Refresh the inventory.';
  if (Object.hasOwn(notices, error?.code)) return notices[error.code];
  return stage === 'contexts'
    ? 'Kubernetes contexts are unavailable. Check your kubeconfig, then refresh.'
    : 'Cluster unavailable. Could not load Nodes. Check that the Kubernetes API is running, then refresh.';
}
export function invalidKubernetesInventory() {
  return Object.assign(Error('Invalid Kubernetes inventory'), {code:'kubernetes_inventory_invalid'});
}
