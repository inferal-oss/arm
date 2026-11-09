// Destination discovery is shared by Install and Connect. A selected Node is
// advisory input; this module deliberately has no effect or decision operation.
import { formatKubeQuantity } from './kube-quantity.mjs';

const text = value => typeof value === 'string' && value.trim().length > 0;
const hash = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
function pinnedServer(value) {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && Boolean(url.hostname) && !url.username && !url.password && url.pathname === '/' && !url.search && !url.hash;
  } catch { return false; }
}
export function exactKubernetesSelection(selection) {
  const { context, cluster, namespace, node } = selection ?? {};
  if (!text(context) || cluster?.context !== context || !pinnedServer(cluster?.server) || !hash(cluster?.ca_sha256) || !text(cluster?.auth_subject) || !Array.isArray(cluster?.auth_groups) || cluster.auth_groups.some(group => !text(group)) || !text(namespace?.name) || !text(namespace?.uid) || !text(node?.name) || !text(node?.uid) || node.ready !== true || node.unschedulable === true || node.taints?.length || node.os !== 'linux' || !['amd64', 'arm64'].includes(node.arch)) {
    throw Error('Select a current ready Linux Node with its exact cluster, Namespace and Node identity.');
  }
  return structuredClone({ context, cluster, namespace, node });
}

export function kubernetesWorkflowNotice(mode) {
  if (mode === 'install') return 'Kubernetes installation reviews are unavailable in this build. You can inspect Nodes and trusted carriers. Selecting a Node does not create a Pod.';
  if (mode === 'connect') return 'Kubernetes connection reviews are unavailable in this build. A running Pod must have verified runtime identity and a pinned connection before it can join the mesh.';
  throw Error('Unsupported Kubernetes destination flow.');
}

function checkedArtifacts(value) {
  const fields = ['sha256', 'size_bytes', 'os', 'arch', 'expected_arm_version', 'expected_lifecycle_protocol'];
  if (!Array.isArray(value) || value.length > 64 || new Set(value.map(row => row?.sha256)).size !== value.length || value.some(row => !row || Object.keys(row).some(key => !fields.includes(key)) || !hash(row.sha256) || !Number.isSafeInteger(row.size_bytes) || row.size_bytes <= 0 || row.size_bytes > 256 * 1024 * 1024 || row.os !== 'linux' || !['amd64', 'arm64'].includes(row.arch) || !text(row.expected_arm_version) || !Number.isSafeInteger(row.expected_lifecycle_protocol) || row.expected_lifecycle_protocol < 1)) throw Error('Invalid artifact metadata');
  return structuredClone(value);
}

export function createKubernetesWorkflowPanel({ ui, call, connected, inventoryFactory, onKubeInventory = () => {}, mode }) {
  const notice = kubernetesWorkflowNotice(mode);
  let active = false, disposed = false, generation = 0;
  const inventoryRoot = ui.stack(), details = ui.stack(), carriers = ui.stack();
  const status = ui.text(notice, { role: 'status' });
  const root = ui.stack([inventoryRoot, status, details, carriers], { hidden: true });
  const inventory = inventoryFactory({ ui, root: inventoryRoot, call, connected, embedded: true, onKubeInventory,
    onRefresh: () => readInventory(inventory.reload),
    onSelection(selection) {
      details.replace();
      if (!selection) return;
      try {
        const exact = exactKubernetesSelection(selection);
        details.append(ui.descriptions([['Selected Node', exact.node.name], ['Namespace UID', exact.namespace.uid], ['Node UID', exact.node.uid], ['Architecture', exact.node.os + ' / ' + exact.node.arch], ['CPU allocatable', formatKubeQuantity(exact.node.allocatable_cpu, 'cpu')], ['RAM allocatable', formatKubeQuantity(exact.node.allocatable_memory)], ['Disk allocatable', formatKubeQuantity(exact.node.allocatable_ephemeral)]]));
      } catch { status.set({ label: 'This Node does not have the exact identity required for a future review. Refresh the inventory.' }); }
    }
  });
  async function readInventory(read) {
    if (disposed || !active) return false;
    const version = ++generation;
    status.set({ label: notice }); carriers.replace();
    const loaded = await read();
    if (disposed || !active || version !== generation || !connected()) return false;
    if (mode === 'install') {
      try {
        const rows = checkedArtifacts(await call('mesh_operator_kubernetes_artifacts'));
        if (disposed || !active || version !== generation || !connected()) return false;
        carriers.replace(ui.heading('Trusted Linux carriers', 3), ...rows.map(row => ui.descriptions([['Platform', row.os + ' / ' + row.arch], ['Binary SHA-256', row.sha256], ['Executable bytes', String(row.size_bytes)], ['ARM version', row.expected_arm_version], ['Lifecycle protocol', String(row.expected_lifecycle_protocol)]])), ui.text(rows.length ? 'Carrier metadata alone does not verify the Pod runtime or authorize installation.' : 'No trusted Linux carrier is imported.'));
      } catch {
        if (!disposed && active && version === generation && connected()) carriers.replace(ui.text('Trusted Linux carriers are unavailable. Node inventory remains read-only.'));
      }
    }
    return loaded;
  }
  function open() {
    if (disposed) return false;
    active = true; root.hidden = false;
    return readInventory(inventory.open);
  }
  function close() {
    active = false; ++generation; root.hidden = true;
    inventory.close(); details.replace(); carriers.replace();
  }
  return { root, inventory, open, close,
    updateConnection() {
      inventory.updateConnection();
      if (!active) return;
      if (connected()) {
        status.set({ label: notice });
      } else {
        ++generation; details.replace(); carriers.replace();
        status.set({ label: 'Disconnected. Refresh the exact Kubernetes selection after reconnecting. ' + notice });
      }
    },
    dispose() {
      if (disposed) return;
      disposed = true; active = false; ++generation; inventory.dispose(); details.replace(); carriers.replace();
    }
  };
}
