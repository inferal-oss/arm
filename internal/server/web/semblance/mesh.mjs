// Mesh workflow state is projected through stock Semblance graph components.
// Backend effects retain exact reviewed keys, revisions, pins and recovery data;
// selecting a component or rendering a preview never authorizes a mutation.
import { nodeStopAvailability, stopDraft, stopReviewIssue, stopPolicyLabel, stopResultLabel, stopResultConfirmed, assertStopOperation, stopOperationTarget } from "../mesh-stop.mjs";
import { nodeUninstallAvailability, uninstallDraft, assertUninstallOperation, uninstallOperationTarget, supportedUninstallReview, uninstallConfirmed, uninstallFinishAllowed } from "../mesh-uninstall.mjs";
import { provisionDraft, validProvisionIntent, assertProvisionOperation, assertProvisionPreparation, supportedProvisionReview, provisionConfirmed } from "../mesh-provision.mjs";
import { bytes, stringifyJSON } from "../core.mjs";
import { validHostID, meshPreparationNotice, phaseLabel, operationAdvice, grantFromDraft, grantDescription, reviewIdentities, canReview, supportedReview, validateMeshReason } from "../mesh-model.mjs";
import { mountNodeAvailability, availabilityObservation } from "./node-availability.mjs";
import { createForwardConsole } from "./forward.mjs";
import { formatKubeQuantity } from "./kube-quantity.mjs";
const stopPolicyOptions = [{ value: "refuse", label: "Refuse while work remains" }, { value: "wait", label: "Wait for existing work" }, { value: "force", label: "Reclaim ARM-owned work" }];
const when = (value) => value ? new Date(value).toLocaleString() : "Not reported";
function createMeshConsole({ ui: graph, root, call, connected, storage, openNode, onKubeInventory = () => {}, onActivity = () => {
}, onChanged = onActivity }) {
  const controls = /* @__PURE__ */ new Set();
  const availabilityMutations = new Map(); // Exact local-host mutation boundaries survive modal remounts.
  const modalParts = /* @__PURE__ */ new Map();
  const button = (label, action, intent = "") => {
    const item = graph.button(label, action, { tone: intent === "danger" ? "Danger" : void 0, emphasis: intent === "primary" ? "Strong" : void 0 });
    controls.add(item);
    return item;
  };
  const message = graph.text("", { role: "status" });
  const refreshButton = button("Refresh nodes", () => refresh(true));
  const connectButton = button("Connect a node\u2026", () => editConnect(), "primary");
  const installButton = button("Install ARM on a host\u2026", () => editProvision());
  const forwardButton = button("TCP forwarding\u2026", () => openForwarding());
  const toolbar = graph.stack([
    graph.heading("Your resource network"),
    graph.text("Each host owns its resources. Access is explicit, directional and revocable."),
    graph.toolbar("Mesh actions", [refreshButton, connectButton, installButton, forwardButton])
  ]);
  const nodes = graph.stack(), operations = graph.stack();
  root.replace(toolbar, message, nodes, operations);
  const kube = createKubeInventory({ ui: graph, root, call, connected, onKubeInventory });
  let rosterKey = "", operationsKey = "", roster = null, allOps = [], selectedHost = "", detail = null, dialog = null, opKey = "", opVersion = 0, renderedRevision = null, renderedSemantic = "", currentOperation = null, lookupVersion = 0, refreshBusy = false, refreshQueued = false, disposed = false;
  let forwardUI = null;
  const announce = (text) => {
    message.set({ label: text });
  };
  let pendingKeys = [], pendingStoreInvalid = false;
  try {
    const saved = storage?.getItem("arm-mesh-pending");
    if (saved) {
      let parsed;
      try {
        parsed = JSON.parse(saved);
      } catch {
        if (/^[A-Za-z0-9_-]{1,128}$/.test(saved)) parsed = [saved];
        else throw Error("Invalid pending key store");
      }
      if (!Array.isArray(parsed) || parsed.length > 16 || parsed.some((k) => typeof k !== "string" || !k.length || k.length > 128)) throw Error("Invalid pending operation store");
      pendingKeys = [...new Set(parsed)];
    }
  } catch {
    pendingStoreInvalid = true;
    announce("Saved pending keys could not be read. Inspect durable Mesh operations before starting another change.");
  }
  let uncertainResumes = /* @__PURE__ */ new Set(), resumeStoreInvalid = false;
  let provisionStoreInvalid = false, provisionPending = { prepares: [], decisions: [] };
  try {
    const saved = JSON.parse(storage?.getItem("arm-mesh-provision-pending") ?? '{"prepares":[],"decisions":[]}');
    if (!saved || Object.keys(saved).sort().join(",") !== "decisions,prepares" || !Array.isArray(saved.prepares) || saved.prepares.length > 16 || saved.prepares.some((q) => !validProvisionIntent(q)) || new Set(saved.prepares.map((q) => q.key)).size !== saved.prepares.length || !Array.isArray(saved.decisions) || saved.decisions.length > 16 || saved.decisions.some((k) => typeof k !== "string" || !/^[a-f0-9]{64}$/.test(k)) || new Set(saved.decisions).size !== saved.decisions.length) throw Error("Invalid installation recovery data");
    provisionPending = saved;
  } catch {
    provisionStoreInvalid = true;
  }
  function retainProvision(kind, key, request = null) {
    if (!storage?.setItem || provisionStoreInvalid || (kind === "prepares" ? provisionPending.prepares.some((q) => q.key === key) : provisionPending.decisions.includes(key)) || provisionPending[kind].length >= 16 || kind === "prepares" && !validProvisionIntent(request)) throw Error("Private installation recovery storage is unavailable or this action is uncertain. Inspect the exact key; no request was sent.");
    const next = { ...provisionPending, [kind]: [...provisionPending[kind], kind === "prepares" ? structuredClone(request) : key] };
    storage.setItem("arm-mesh-provision-pending", JSON.stringify(next));
    provisionPending = next;
  }
  function settleProvision(op) {
    const next = { prepares: op.review || ["rejected", "declined", "cancelled"].includes(op.phase) ? provisionPending.prepares.filter((q) => q.key !== op.key) : provisionPending.prepares, decisions: op.approved || op.lookup_only || ["rejected", "declined", "cancelled"].includes(op.phase) ? provisionPending.decisions.filter((k) => k !== op.key) : provisionPending.decisions };
    if (stringifyJSON(next) === stringifyJSON(provisionPending)) return;
    try {
      storage.setItem("arm-mesh-provision-pending", JSON.stringify(next));
      provisionPending = next;
    } catch {
      provisionStoreInvalid = true;
    }
  }
  const provisionReference = (op) => op ? { target: op.targets?.[0], plan: op.review ?? null, hash: op.review_sha256, revision: op.review_revision, provisional: op.phase === "preparing" && !op.approved && !op.lookup_only } : null;
  function assertProvisionCurrent(op, key) {
    assertProvisionOperation(op, key, currentOperation?.kind === "provision" && currentOperation.key === key ? provisionReference(currentOperation) : null);
    const original = provisionPending.prepares.find((q) => q.key === key);
    return original ? assertProvisionPreparation(op, original) : op;
  }
  try {
    const saved = JSON.parse(storage?.getItem("arm-mesh-resume-pending") ?? "[]");
    if (!Array.isArray(saved) || saved.length > 16 || saved.some((key) => typeof key !== "string" || !key.length || key.length > 128)) throw Error("Invalid recovery intent store");
    uncertainResumes = new Set(saved);
  } catch {
    resumeStoreInvalid = true;
  }
  let uncertainStopDecisions = /* @__PURE__ */ new Set(), stopStoreInvalid = false;
  try {
    const saved = JSON.parse(storage?.getItem("arm-mesh-stop-decisions") ?? "[]");
    if (!Array.isArray(saved) || saved.length > 16 || saved.some((key) => typeof key !== "string" || !key.length || key.length > 128)) throw Error("Invalid Stop decision store");
    uncertainStopDecisions = new Set(saved);
  } catch {
    stopStoreInvalid = true;
  }
  function retainStopDecision(key) {
    if (!storage?.setItem || stopStoreInvalid || !uncertainStopDecisions.has(key) && uncertainStopDecisions.size >= 16) throw Error("Private browser Stop-decision recovery storage is unavailable. No request was sent.");
    const next = new Set(uncertainStopDecisions);
    next.add(key);
    storage.setItem("arm-mesh-stop-decisions", JSON.stringify([...next]));
    uncertainStopDecisions = next;
  }
  function settleStopDecision(key) {
    if (!uncertainStopDecisions.has(key)) return;
    const next = new Set(uncertainStopDecisions);
    next.delete(key);
    try {
      storage.setItem("arm-mesh-stop-decisions", JSON.stringify([...next]));
      uncertainStopDecisions = next;
    } catch {
      stopStoreInvalid = true;
    }
  }
  let uninstallStoreInvalid = false, uninstallPending = { decisions: [], finishes: [] };
  const uninstallLookupReady = /* @__PURE__ */ new Map();
  const pendingFinish = (key) => uninstallPending.finishes.find((v) => v.key === key);
  const finishReady = (op) => uninstallFinishAllowed(op) && op.error?.code !== "source_owner_unresolved" && uninstallLookupReady.get(op.key) === stringifyJSON([op.revision, op.review_revision, op.review_sha256]);
  try {
    const saved = JSON.parse(storage?.getItem("arm-mesh-uninstall-pending") ?? '{"decisions":[],"finishes":[]}');
    if (!saved || Object.keys(saved).sort().join(",") !== "decisions,finishes" || !Array.isArray(saved.decisions) || !Array.isArray(saved.finishes) || saved.decisions.length > 16 || saved.finishes.length > 16 || saved.decisions.some((v) => typeof v !== "string" || !/^[A-Za-z0-9_-]{1,128}$/.test(v)) || saved.finishes.some((v) => !v || Object.keys(v).sort().join(",") !== "key,revision,review_sha256" || !/^[A-Za-z0-9_-]{1,128}$/.test(v.key ?? "") || typeof v.revision !== "string" || !/^[1-9][0-9]{0,18}$/.test(v.revision) || BigInt(v.revision) > 9223372036854775807n || !/^[a-f0-9]{64}$/.test(v.review_sha256 ?? "")) || new Set(saved.decisions).size !== saved.decisions.length || new Set(saved.finishes.map((v) => v.key)).size !== saved.finishes.length) throw Error("Invalid uninstall recovery data");
    uninstallPending = saved;
  } catch {
    uninstallStoreInvalid = true;
  }
  function retainUninstall(kind, key, op = null) {
    if (!storage?.setItem || uninstallStoreInvalid || (kind === "finishes" ? pendingFinish(key) : uninstallPending.decisions.includes(key)) || uninstallPending[kind].length >= 16) throw Error("The exact uninstall action is uncertain or private recovery storage is unavailable. Inspect its receipt; no request was sent.");
    const item = kind === "finishes" ? { key, revision: String(op.revision), review_sha256: op.review_sha256 } : key;
    const next = { ...uninstallPending, [kind]: [...uninstallPending[kind], item] };
    storage.setItem("arm-mesh-uninstall-pending", JSON.stringify(next));
    uninstallPending = next;
  }
  function settleUninstall(op, nativeLookup = false) {
    const decisions = op.approved || op.lookup_only || ["rejected", "declined", "cancelled"].includes(op.phase) ? uninstallPending.decisions.filter((k) => k !== op.key) : uninstallPending.decisions;
    const pending = pendingFinish(op.key), resolved = uninstallConfirmed(op) || nativeLookup && pending && finishReady(op) && pending.review_sha256 === op.review_sha256 && BigInt(op.revision) > BigInt(pending.revision);
    const finishes = resolved ? uninstallPending.finishes.filter((v) => v.key !== op.key) : uninstallPending.finishes;
    if (decisions.length === uninstallPending.decisions.length && finishes.length === uninstallPending.finishes.length) return;
    try {
      storage.setItem("arm-mesh-uninstall-pending", JSON.stringify({ decisions, finishes }));
      uninstallPending = { decisions, finishes };
    } catch {
      uninstallStoreInvalid = true;
    }
  }
  function retainStopPrepare(key) {
    if (!storage?.setItem || pendingStoreInvalid || pendingKeys.length >= 16) throw Error("Durable operation-key storage is unavailable. No stop preparation was sent.");
    const next = [.../* @__PURE__ */ new Set([...pendingKeys, key])];
    storage.setItem("arm-mesh-pending", JSON.stringify(next));
    pendingKeys = next;
  }
  function reviewAllowed(op) {
    return canReview(op) && !(op.kind === "provision" && (provisionStoreInvalid || provisionPending.decisions.includes(op.key))) && !(op.kind === "stop" && (stopStoreInvalid || uncertainStopDecisions.has(op.key))) && !(op.kind === "uninstall" && (uninstallStoreInvalid || uninstallPending.decisions.includes(op.key)));
  }
  function declineAllowed(op) {
    return reviewAllowed(op) || ["stop", "uninstall"].includes(op.kind) && op.phase === "awaiting_review" && op.worker_live && !op.approved && !op.lookup_only && (op.kind === "stop" ? !stopStoreInvalid && !uncertainStopDecisions.has(op.key) : !uninstallStoreInvalid && !uninstallPending.decisions.includes(op.key)) && Number(op.review_revision) > 0 && /^[a-f0-9]{64}$/.test(op.review_sha256 ?? "") && Date.now() < Date.parse(op.review_expires_at);
  }
  function retainResume(key) {
    if (!storage?.setItem) throw Error("Durable recovery intent storage is unavailable; no request was sent.");
    if (resumeStoreInvalid || !uncertainResumes.has(key) && uncertainResumes.size >= 16) throw Error("Recovery intent storage needs inspection before another recovery request.");
    uncertainResumes.add(key);
    storage?.setItem("arm-mesh-resume-pending", JSON.stringify([...uncertainResumes]));
  }
  function settleResume(key) {
    uncertainResumes.delete(key);
    try {
      storage?.setItem("arm-mesh-resume-pending", JSON.stringify([...uncertainResumes]));
    } catch {
      resumeStoreInvalid = true;
    }
  }
  function savePending() {
    try {
      storage?.setItem("arm-mesh-pending", JSON.stringify(pendingKeys));
    } catch {
      announce("Pending key storage is unavailable. Keep this tab open and retain exact operation keys before leaving.");
    }
  }
  const remember = (key) => {
    if (pendingStoreInvalid) throw Error("Saved pending-key data is unreadable; inspect durable operations before explicitly clearing this browser cache.");
    if (!pendingKeys.includes(key)) {
      if (pendingKeys.length >= 16) throw Error("16 uncertain operations are retained. Inspect them before preparing another change.");
      pendingKeys.push(key);
      savePending();
    }
  };
  const forget = (key) => {
    if (pendingKeys.includes(key)) {
      pendingKeys = pendingKeys.filter((k) => k !== key);
      savePending();
    }
  };
  function modal(title, context = "MESH / LOCAL OPERATOR") {
    if (dialog?.open) dialog.close();
    const opener = graph.activeComponent;
    const body = graph.stack(), foot = graph.stack([], { row: true });
    const d = graph.dialog(title, [graph.text(context), body, foot]);
    const heading = { set: (props) => d.set(props) };
    const parts = { d, body, foot, heading };
    modalParts.set(d, parts);
    dialog = d;
    d.on("close", () => {
      const current = dialog === d;
      if (current) {
        dialog = null;
        opKey = "";
        opVersion++;
      }
      parts.availability?.dispose();
      modalParts.delete(d);
      d.dispose();
      if (current) {
        if (opener?.isConnected) opener.focus();
        else connectButton.focus();
      }
    });
    d.showModal();
    return parts;
  }
  function openForwarding() {
    const ui = modal("TCP source forwarding", "MESH / LOCAL SOURCE LISTENERS");
    const current = createForwardConsole({ ui: graph, root: ui.body, call, connected });
    forwardUI = current;
    ui.d.on("close", () => {
      current.dispose();
      if (forwardUI === current) forwardUI = null;
    });
    ui.foot.append(button("Close", () => ui.d.close()));
    current.refresh();
  }
  function note(parent, text, kind = "") {
    parent.append(graph.text(text, { tone: kind === "warning" ? "Warning" : void 0 }));
  }
  function row(parent, label, value, mono = false) {
    const r = graph.stack([graph.text(label), graph.text(String(value ?? "Not reported"))], { row: true });
    parent.append(r);
  }
  function grantCard(title, grant) {
    const box = graph.card(title, [graph.text(grantDescription(grant))]);
    if (grant?.permissions?.includes("lease-own")) {
      const facts = graph.stack();
      row(facts, "Aggregate ceilings", `${grant.limits?.cpu ?? 0} CPU \xB7 ${bytes(grant.limits?.memory_bytes ?? 0)} memory \xB7 ${bytes(grant.limits?.disk_bytes ?? 0)} disk`);
      row(facts, "Maximum lease", `${Number(grant.max_ttl_ns ?? 0) / 6e10} minutes`);
      box.append(facts);
    }
    if (grant?.permissions?.includes("message")) note(box, `Messages: ${grant.operator_inbox ? "operator inbox" : ""}${grant.operator_inbox && grant.contact_ids?.length ? " + " : ""}${grant.contact_ids?.length ? grant.contact_ids.length + " exact contacts" : ""}`);
    return box;
  }
  function renderRoster() {
    const key = stringifyJSON((roster?.nodes ?? []).map((n) => [n.host, n.name, n.display_name, n.local, n.health, n.trust?.state, n.trust?.execution_account, n.metadata?.platform, n.metadata?.build, n.metadata?.inventory?.logical_cpus, n.metadata?.inventory?.host_memory?.total_bytes, n.metadata_freshness?.stale, n.error].map((value) => value ?? null)));
    if (key === rosterKey) return;
    rosterKey = key;
    const count = roster?.nodes?.length ?? 0;
    nodes.replace();
    if (!count) {
      nodes.append(graph.text("No node inventory yet. Refresh reads the registry without creating mesh state."));
      return;
    }
    const cards = [];
    for (const entry of roster.nodes) {
      const title = entry.display_name || entry.name || `Node ${entry.host.slice(0, 8)}`;
      const metadata = entry.metadata, platform = metadata?.platform;
      const facts = [
        ["Status", entry.health ?? "Unknown"],
        ["Run as", entry.trust?.execution_account?.username || entry.trust?.execution_account?.uid || "Not reported"]
      ];
      if (metadata) facts.push(["Inventory", `${metadata.inventory?.logical_cpus?.value ?? metadata.inventory?.logical_cpus ?? "\u2014"} CPU \xB7 ${bytes(metadata.inventory?.host_memory?.total_bytes)}`]);
      const actions = [button("View node", () => inspect(entry.host))];
      if (openNode) actions.push(button("Open dashboard \u2197", () => openNode(entry)));
      if (openNode && (platform?.os === "darwin" || !metadata && entry.trust?.state === "active")) actions.push(button("Screen \u2197", () => openNode(entry, "screen"), "primary"));
      if (!entry.local && entry.trust?.state === "revoked") actions.push(button("Reconnect\u2026", () => editConnect(entry), "primary"));
      const card = graph.card(title, [
        graph.badge(entry.local ? "This node" : entry.trust?.state ?? "Unknown"),
        graph.text(platform ? `${platform.os} / ${platform.process_architecture} \xB7 ARM ${metadata.build?.arm_version ?? "unknown"}${entry.metadata_freshness?.stale ? " \xB7 cached / stale" : ""}` : "Metadata not yet read"),
        graph.descriptions(facts),
        graph.disclosure(`Host identity \xB7 ${entry.host.slice(0, 8)}\u2026`, [graph.descriptions([["Stable host ID", entry.host]])])
      ], { actions: [graph.toolbar(`Actions for ${title}`, actions)] });
      graph.put?.(card.iri, "http://www.w3.org/1999/02/22-rdf-syntax-ns#type", "https://arm.local/ui#NodeRosterCard", true);
      cards.push(card);
    }
    const grid = graph.grid(cards);
    graph.put?.(grid.iri, "http://www.w3.org/1999/02/22-rdf-syntax-ns#type", "https://arm.local/ui#NodeRosterGrid", true);
    nodes.append(grid);
    if (roster.error) announce(roster.error.message);
  }
  async function inspect(host) {
    if (!validHostID(host)) return;
    selectedHost = host;
    const ui = modal("Node details", "MESH / NODE"), status = graph.text("Reading this exact host\u2026");
    // Scope only this native Dialog through its existing noninteractive bodies.
    for (const body of [ui.body, ui.d.body]) if (body) graph.put?.(body.iri, "http://www.w3.org/1999/02/22-rdf-syntax-ns#type", "https://arm.local/ui#NodeDetailBody", true);
    graph.put?.(ui.foot.iri, "http://www.w3.org/1999/02/22-rdf-syntax-ns#type", "https://arm.local/ui#NodeDetailFoot", true);
    ui.body.append(status);
    detail = ui;
    try {
      const entry = await call("node_inspect", { host });
      if (disposed || dialog !== ui.d || selectedHost !== host) return;
      if (entry?.host !== host) throw Error("Node inspection differs from the exact requested host.");
      let observation = null;
      try { observation = await call("node_availability_inspect_v1", {host}); } catch { /* Optional older gateway. */ }
      if (disposed || dialog !== ui.d || selectedHost !== host) return;
      renderNode(ui, entry, observation);
    } catch (error) {
      if (dialog === ui.d) status.set({ label: error.message });
    }
  }
  function renderNode(ui, entry, observation) {
    ui.availability?.dispose();
    ui.heading.set({ label: entry.display_name || entry.name || entry.host });
    ui.body.replace();
    ui.body.append(graph.descriptions([
      ["Stable host ID", entry.host],
      ["Authority", entry.local ? "This local backend" : "Direct peer through this local gateway"],
      ["Connection", entry.health ?? "Not reported"],
      ["Run as", entry.trust?.execution_account?.username ? `${entry.trust.execution_account.username} (UID ${entry.trust.execution_account.uid})` : entry.trust?.execution_account?.uid ?? "Not reported"]
    ]));
    if (!entry.local) {
      note(ui.body, entry.scope_freshness?.stale ? "Permissions are stale or unavailable. A fresh destination check is required for actions." : "Permissions below are this source\u2019s current observed rights, enforced by the destination.", entry.scope_freshness?.stale ? "warning" : "");
      const grants = graph.grid([grantCard("We may do on this node", entry.outbound_scope?.grant), grantCard("This node may do here", entry.trust?.inbound_grant)]);
      graph.put?.(grants.iri, "http://www.w3.org/1999/02/22-rdf-syntax-ns#type", "https://arm.local/ui#NodeDetailGrants", true);
      ui.body.append(grants);
      const trust = graph.stack();
      graph.put?.(trust.iri, "http://www.w3.org/1999/02/22-rdf-syntax-ns#type", "https://arm.local/ui#NodeDetailTrust", true);
      row(trust, "Endpoint", entry.trust?.address, true);
      row(trust, "Enrollment", entry.trust?.pair, true);
      row(trust, "Certificate SHA-256", entry.trust?.certificate_fingerprint, true);
      row(trust, "Public key SHA-256", entry.trust?.spki_sha256, true);
      row(trust, "Certificate expires", when(entry.trust?.expires_at));
      ui.body.append(trust);
    }
    if (entry.metadata) {
      const label = graph.heading("Reported node metadata", 3);
      ui.body.append(label);
      note(ui.body, "Reported capabilities describe installed software, not permission to use it. Inventory timestamps describe measurement freshness.");
      ui.body.append(graph.json(entry.metadata, "Reported metadata"));
    } else note(ui.body, "This peer has not provided metadata. No local data is substituted.");
    if (entry.error) note(ui.body, entry.error.message, "warning");
    const availabilityRoot = graph.stack();
    ui.body.append(availabilityRoot);
    let mutationState = availabilityMutations.get(entry.host);
    if (!mutationState) {mutationState={epoch:0,pending:false,uncertain:false};availabilityMutations.set(entry.host,mutationState);}
    ui.availability = mountNodeAvailability({ui: graph, root: availabilityRoot, host: entry.host, sourceHost: roster?.source_host, local: entry.local, connected, mutationState,
      ...availabilityObservation(entry.host, observation),
      read: () => call("node_availability_read_v1", {host: entry.host}),
      inspect: () => call("node_availability_inspect_v1", {host: entry.host}),
      setSelf: entry.local ? request => call("node_availability_set_v1", request) : undefined});
    ui.foot.replace(button("Close", () => ui.d.close()));
    if (openNode) ui.foot.append(button("Open dashboard \u2197", () => openNode(entry), "primary"));
    if (!entry.local && entry.trust?.state === "active") ui.foot.append(button("Disconnect this node\u2026", () => editDisconnect(entry), "danger"));
    if (!entry.local && entry.trust?.state === "revoked") ui.foot.append(button("Reconnect this node\u2026", () => editConnect(entry), "primary"));
    if (!entry.local) {
      const available = nodeStopAvailability(entry), stop = button("Stop ARM\u2026", () => editStop(entry), "danger");
      stop.disabled = !available.enabled || stopStoreInvalid || uncertainStopDecisions.size >= 16;
      stop.set({ help: stopStoreInvalid || uncertainStopDecisions.size >= 16 ? "Inspect retained Stop-decision recovery records before another stop review." : available.reason });
      ui.foot.append(stop);
      if (!available.enabled) note(ui.body, available.reason);
    }
    if (!entry.local) {
      const available = nodeUninstallAvailability(entry), remove = button("Uninstall managed ARM\u2026", () => editUninstall(entry), "danger");
      remove.disabled = !available.enabled || uninstallStoreInvalid;
      remove.set({ help: available.reason });
      ui.foot.append(remove);
      if (!available.enabled) note(ui.body, available.reason);
    }
  }
  function field(parent, label, { type = "text", value = "", placeholder = "", required = false } = {}) {
    const input = type === "textarea" ? graph.area(label, { value, placeholder, required, maxLength: 4096 }) : type === "checkbox" ? graph.check(label) : graph.field(label, { type, value, placeholder, required });
    parent.append(input);
    return input;
  }
  function grantEditor(parent, title, initial) {
    const mode = graph.select("Permission level", [
      { value: "none", label: "No access" },
      { value: "summary", label: "Host summary only" },
      { value: "leases", label: "Our leases + resource reservations" },
      { value: "run", label: "Our leases + run programs" }
    ], { value: initial });
    const limits = graph.stack(), cpu = field(limits, "CPU ceiling (cores)", { type: "number", value: "1" }), memory = field(limits, "Memory ceiling (GiB)", { type: "number", value: "1" }), disk = field(limits, "Disk ceiling (GiB)", { type: "number", value: "2" }), ttl = field(limits, "Maximum lease (minutes)", { type: "number", value: "10" });
    for (const input of [cpu, memory, disk, ttl]) input.set({ min: 0, step: "any" });
    const messaging = graph.disclosure("Message access (optional)"), allow = field(messaging, "Allow messages", { type: "checkbox" }), inbox = field(messaging, "Allow operator inbox", { type: "checkbox" }), contacts = field(messaging, "Exact allowed contact IDs", { placeholder: "Contact IDs separated by commas" });
    const screen = graph.check("Allow Standard Screen Sharing (requires Mac sign-in)");
    const warning = graph.text("Run executes as the destination OS account, with its file and credential access. It is not a sandbox.", { tone: "Warning" });
    const box = graph.card(title, [mode, limits, messaging, screen, warning]);
    const update = () => {
      limits.hidden = !["leases", "run"].includes(mode.value);
      for (const input of [cpu, memory, disk, ttl]) input.disabled = limits.hidden;
      warning.hidden = mode.value !== "run";
      messaging.hidden = mode.value === "none";
      screen.disabled = mode.value === "none";
    };
    mode.on("change", update);
    update();
    parent.append(box);
    return () => grantFromDraft({ mode: mode.value, cpu: cpu.value, memory: memory.value, disk: disk.value, ttl: ttl.value, messages: allow.checked, operatorInbox: inbox.checked, contacts: contacts.value, screenView: screen.checked });
  }
  async function editProvision() {
    if (provisionStoreInvalid) {
      announce("Inspect unreadable installation recovery data before another install.");
      return;
    }
    const ui = modal("Install ARM on a host", "MESH / USER-LEVEL INSTALLATION"), form = graph.stack(), error = graph.text("", { role: "alert" });
    error.set({ role: "alert" });
    note(form, "ARM uses your verified SSH trust to inspect the account and platform, then selects a trusted local companion. Review the exact executable, paths and startup settings before any installation.");
    const source = graph.select("Connection method", [{ value: "", label: "SSH destination" }], { value: "" });
    form.append(source);
    const destination = field(form, "Verified SSH destination", { required: true, placeholder: "user@host or a configured SSH alias" }), reason = field(form, "Reason for installing ARM", { type: "textarea", required: true });
    note(form, "Installation starts a user-level ARM node. Mesh trust and permissions require a separate review. No private keys are copied.");
    form.append(error);
    ui.body.append(form);
    const keep = button("Keep host unchanged", () => ui.d.close()), prepare = button("Prepare installation review", () => {
    }, "primary");
    keep.localDismiss = true;
    ui.foot.append(keep, prepare);
    keep.focus();
    call("mesh_operator_connectors").then((profiles) => {
      if (dialog !== ui.d) return;
      source.set({ options: [{ value: "", label: "SSH destination" }, ...(profiles ?? []).map((profile) => ({ value: profile.id, label: `${profile.id} \xB7 ${profile.ssh_destination}` }))] });
    }).catch((e) => {
      if (dialog === ui.d) note(form, `Reviewed connectors unavailable: ${e.message}`, "warning");
    });
    source.on("change", () => {
      destination.disabled = Boolean(source.value);
    });
    prepare.on("activate", async () => {
      if (dialog !== ui.d || !ui.d.open || prepare.disabled || !connected()) return;
      prepare.disabled = true;
      let q;
      try {
        q = await provisionDraft(crypto.randomUUID(), { ...source.value ? { connector: source.value } : { destination: destination.value.trim() }, reason: reason.value });
        if (dialog !== ui.d || !ui.d.open) return;
        retainProvision("prepares", q.key, q);
      } catch (e) {
        error.set({ label: e.message });
        prepare.disabled = false;
        return;
      }
      try {
        const op = assertProvisionPreparation(await call("mesh_operator_provision_prepare", q), q);
        settleProvision(op);
        if (dialog === ui.d) {
          ui.d.close();
          showOperation(op);
        } else {
          announce(`Installation ${q.key} recorded. Inspect its exact receipt.`);
          refresh();
        }
      } catch (e) {
        error.set({ label: `${e.message}. Installation preparation may be recorded as ${q.key}. Look up that exact key; it will not be resent.` });
        renderOperations();
      }
    });
  }
  async function editConnect(entry = null, installed = null) {
    if (installed && !provisionConfirmed(installed)) return;
    const ui = modal(installed ? "Connect the installed ARM node" : entry ? "Reconnect this exact node" : "Connect a node", entry ? "MESH / NEW ENROLLMENT" : "MESH / NEW CONNECTION"), form = graph.stack(), error = graph.text("", { role: "alert" });
    error.set({ role: "alert" });
    note(form, meshPreparationNotice);
    if (entry) {
      const exact = graph.stack();
      row(exact, "Exact node", entry.display_name || entry.name || entry.host);
      row(exact, "Stable host", entry.host, true);
      row(exact, "Previous enrollment", entry.trust?.pair, true);
      form.append(exact);
      note(form, "This creates a new enrollment. Previous grants are context only; choose new permissions below. Pending disconnect receipts and all old resource obligations remain.", "warning");
    }
    const target = graph.stack();
    target.append(graph.heading("1. Choose a destination", 3));
    const source = graph.select("Connection method", [{ value: "", label: "SSH destination" }], { value: "" });
    target.append(source);
    const destination = field(target, entry ? "Replacement SSH destination (optional)" : "Verified SSH destination", { placeholder: entry ? "Blank uses the exact retained verified locator" : "user@host or a configured SSH alias", required: !entry });
    note(target, "Uses your existing known_hosts trust. ARM must already be installed there; no credentials or private keys are copied.");
    if (installed) {
      source.disabled = destination.disabled = true;
      destination.value = installed.review.request.ssh_destination;
      const exact = graph.stack();
      row(exact, "Original installation", installed.key, true);
      row(exact, "Verified started node", installed.result.receipt.started_host, true);
      target.append(exact);
      note(target, "This connection uses the original verified installation. The backend rechecks its SSH pin, account and started node before preparing trust.");
    }
    const reason = field(target, "Reason for connecting", { type: "textarea", required: true, placeholder: "What will this connection be used for?" });
    form.append(target);
    call("mesh_operator_connectors").then((profiles) => {
      if (dialog !== ui.d) return;
      source.set({ options: [{ value: "", label: "SSH destination" }, ...(profiles ?? []).map((profile) => ({ value: profile.id, label: `${profile.id} \xB7 ${profile.ssh_destination}` }))] });
    }).catch((e) => {
      if (dialog === ui.d) note(target, `Reviewed connectors unavailable: ${e.message}`, "warning");
    });
    source.on("change", () => {
      if (installed) return;
      destination.disabled = Boolean(source.value);
    });
    const directions = graph.stack();
    directions.append(graph.heading("2. Set directional permissions", 3));
    const grid = graph.stack(), forward = grantEditor(grid, "This node \u2192 destination", "summary"), reverse = grantEditor(grid, "Destination \u2192 this node", "none");
    directions.append(grid);
    form.append(directions);
    const replace = entry ? null : field(form, "Explicitly replace a previously revoked enrollment, if present", { type: "checkbox" });
    form.append(error);
    ui.body.append(form);
    const cancel = button("Cancel", () => ui.d.close()), prepare = button(entry ? "Prepare reconnect review" : "Prepare connection review", () => {
    }, "primary");
    ui.foot.append(cancel, prepare);
    cancel.focus();
    let contextReady = !entry;
    if (entry) {
      prepare.disabled = true;
      call("mesh_operator_reconnect_context", { host: entry.host }).then((context) => {
        if (dialog !== ui.d) return;
        if (context.host !== entry.host || !Array.isArray(context.blockers)) throw Error("Exact reconnect context unavailable");
        if (context.blockers.length) {
          note(form, "A previous workflow still owns this node. Resolve it or explicitly hand off its workflow exclusion, then return for a fresh reconnect review. Old obligations remain.", "warning");
          for (const pending of context.blockers) {
            note(form, `${pending.kind} \xB7 ${pending.phase}`);
            form.append(button(`Inspect pending operation ${pending.key}`, () => lookup(pending.key)));
          }
        } else {
          contextReady = true;
          prepare.disabled = false;
        }
      }).catch((e) => {
        if (dialog === ui.d) error.set({ label: e.message });
      });
    }
    prepare.on("activate", async () => {
      error.set({ label: "" });
      if (!contextReady) {
        error.set({ label: "Inspect and resolve the pending workflow before preparing a fresh reconnect." });
        return;
      }
      if (!connected()) {
        error.set({ label: "Reconnect and inspect before preparing a change." });
        return;
      }
      if (!reason.value.trim() || !entry && !source.value && !destination.value.trim()) {
        error.set({ label: "A destination and a meaningful reason are required." });
        return;
      }
      if (prepare.disabled || dialog !== ui.d || !ui.d.open) return;
      let q;
      try {
        validateMeshReason(reason.value);
        const draft = { ...installed ? { provision_key: installed.key } : source.value ? { connector: source.value } : destination.value.trim() ? { ssh_destination: destination.value.trim() } : {}, reason: reason.value, source_permissions_on_target: forward(), target_permissions_on_source: reverse() };
        q = entry ? { key: crypto.randomUUID(), kind: "reconnect", reconnect: { ...draft, host: entry.host } } : { key: installed ? installed.review.request.connection_key : crypto.randomUUID(), kind: "connect", connect: { ...draft, replace_revoked: Boolean(replace.checked) } };
      } catch (e) {
        error.set({ label: e.message });
        return;
      }
      try {
        if (installed) {
          if (pendingKeys.includes(q.key)) throw Error("This linked connection is uncertain. Inspect its exact key before preparing anything again.");
          retainStopPrepare(q.key);
        } else remember(q.key);
      } catch (e) {
        error.set({ label: e.message });
        return;
      }
      prepare.disabled = true;
      try {
        const op = await call("mesh_operator_prepare", q);
        if (installed && (op.kind !== "connect" || op.key !== q.key)) throw Error("The connection receipt differs from the original installation key.");
        if (op.kind === "provision") assertProvisionCurrent(op, op.key);
        if (dialog === ui.d) {
          ui.d.close();
          showOperation(op);
        } else {
          announce(`Operation ${op.key} recorded. View its receipt under Mesh operations.`);
          refresh();
        }
      } catch (e) {
        error.set({ label: `${e.message}. The exact operation key is ${q.key}. Inspect its receipt before preparing anything again.` });
        announce(`Connection outcome requires lookup: ${q.key}`);
        renderOperations();
      }
    });
  }
  function editStop(entry) {
    if (stopStoreInvalid || uncertainStopDecisions.size >= 16) {
      announce("Inspect the retained Stop-decision recovery records before preparing another stop review.");
      return;
    }
    const available = nodeStopAvailability(entry);
    if (!available.enabled) {
      announce(available.reason);
      return;
    }
    const ui = modal("Stop ARM on this node", "MESH / BACKEND SHUTDOWN"), facts = graph.stack();
    row(facts, "Node", entry.display_name || entry.name || entry.host);
    row(facts, "Stable host", entry.host, true);
    ui.body.append(facts);
    note(ui.body, "Stops this ARM backend and all its connections. The host remains running. A separate concrete review follows before any stop is approved.", "warning");
    const form = graph.stack();
    const mode = graph.select("Stop policy", stopPolicyOptions, { value: "refuse" });
    form.append(mode);
    const timeout = field(form, "Reclaim after (whole seconds; blank means no timeout)", { value: "", placeholder: "Optional while waiting" }), reason = field(form, "Why should this ARM backend stop?", { type: "textarea", required: true }), policyNote = graph.text(""), error = graph.text("", { role: "alert" });
    error.set({ role: "alert" });
    form.append(policyNote, error);
    ui.body.append(form);
    const keep = button("Keep running", () => ui.d.close()), prepare = button("Prepare stop review", () => {
    }, "danger");
    keep.localDismiss = true;
    ui.foot.append(keep, prepare);
    keep.focus();
    const update = () => {
      timeout.disabled = mode.value !== "wait";
      if (timeout.disabled) timeout.value = "";
      timeout.hidden = timeout.disabled;
      policyNote.set({ label: mode.value === "force" ? "Requests reclamation of ARM-owned work only. Foreign processes, unverified storage and unresolved work are never discarded." : mode.value === "wait" ? "Waiting denies new work while existing obligations settle. A positive timeout requests ARM-owned reclamation after that interval." : "Outstanding work or cleanup prevents this default stop." });
    };
    mode.on("change", update);
    update();
    prepare.on("activate", async () => {
      if (disposed || dialog !== ui.d || !ui.d.open || prepare.disabled) return;
      if (!connected()) {
        error.set({ label: "Reconnect and refresh this node before preparing a stop." });
        return;
      }
      let q;
      try {
        const draft = stopDraft(entry.host, reason.value, mode.value, mode.value === "wait" ? timeout.value : "");
        q = { key: crypto.randomUUID(), kind: "stop", stop: draft };
        retainStopPrepare(q.key);
      } catch (e) {
        error.set({ label: e.message });
        return;
      }
      prepare.disabled = true;
      try {
        const op = await call("mesh_operator_prepare", q);
        assertStopOperation(op, q.key, entry.host);
        if (dialog === ui.d) {
          ui.d.close();
          showOperation(op);
        } else {
          announce(`Stop preparation is recorded as ${q.key}. Inspect its exact receipt.`);
          refresh();
        }
      } catch (e) {
        error.set({ label: `${e.message}. Stop preparation may be recorded as ${q.key}. Look up that exact key; it will not be resent.` });
        renderOperations();
      }
    });
  }
  async function editUninstall(entry) {
    const available = nodeUninstallAvailability(entry);
    if (!available.enabled || uninstallStoreInvalid) {
      announce(available.reason || "Inspect saved uninstall recovery records before another change.");
      return;
    }
    const ui = modal("Choose a managed installation", "MESH / UNINSTALL"), form = graph.stack(), error = graph.text("", { role: "alert" });
    error.set({ role: "alert" });
    const facts = graph.stack();
    row(facts, "Destination", entry.display_name || entry.name || entry.host);
    row(facts, "Stable host", entry.host, true);
    ui.body.append(facts);
    note(ui.body, "Remove one original ARM-managed executable after its exact backend stops. Database, identity, history, configuration, backups and recovery helper stay. A concrete review follows.", "warning");
    const choice = graph.select("Original installation", [{ value: "", label: "Choose an original managed installation" }], { value: "" });
    form.append(choice);
    const selected = graph.stack(), mode = graph.select("Stop policy before removal", stopPolicyOptions, { value: "refuse" });
    form.append(selected, mode);
    const timeout = field(form, "Reclaim after (whole seconds; blank means no timeout)", { placeholder: "Optional while waiting" }), reason = field(form, "Why remove this managed installation?", { type: "textarea", required: true });
    form.append(error);
    ui.body.append(form);
    const keep = button("Keep installed", () => ui.d.close()), prepare = button("Prepare uninstall review", () => {
    }, "danger");
    prepare.disabled = true;
    keep.localDismiss = true;
    ui.foot.append(keep, prepare);
    keep.focus();
    let candidates = [], preparing = false;
    const update = () => {
      timeout.disabled = mode.value !== "wait";
      timeout.hidden = timeout.disabled;
      if (timeout.disabled) timeout.value = "";
      selected.replace();
      const item = candidates.find((v) => v.provision_key === choice.value);
      if (item) {
        row(selected, "ARM home", item.home, true);
        row(selected, "Managed executable", item.binary, true);
        row(selected, "Platform / size", `${item.os} / ${item.arch} \xB7 ${bytes(item.size_bytes)}`);
      }
      prepare.disabled = preparing || !item;
    };
    choice.on("change", update);
    mode.on("change", update);
    update();
    try {
      const context = await call("mesh_operator_uninstall_context", { host: entry.host });
      if (dialog !== ui.d || !ui.d.open) return;
      if (context.host !== entry.host || !Array.isArray(context.candidates) || context.candidates.length > 64 || new Set(context.candidates.map((v) => v.provision_key)).size !== context.candidates.length || context.candidates.some((v) => !v || typeof v.provision_key !== "string" || !/^[A-Za-z0-9_-]{1,128}$/.test(v.provision_key) || typeof v.home !== "string" || !v.home.startsWith("/") || typeof v.binary !== "string" || !v.binary.startsWith("/") || !/^[a-f0-9]{64}$/.test(v.sha256 ?? "") || !["darwin", "linux"].includes(v.os) || !["arm64", "amd64"].includes(v.arch) || !(typeof v.size_bytes === "bigint" ? v.size_bytes > 0n && v.size_bytes <= 268435456n : Number.isSafeInteger(v.size_bytes) && v.size_bytes > 0 && v.size_bytes <= 268435456))) throw Error("Original installation context is incomplete or differs from the selected host.");
      candidates = context.candidates;
      choice.set({ options: [{ value: "", label: "Choose an original managed installation" }, ...candidates.map((item) => ({ value: item.provision_key, label: `${item.provision_key} \xB7 ${item.os} / ${item.arch} \xB7 ${item.home}` }))] });
      if (!candidates.length) note(form, context.reason || "No original ARM-managed installation is eligible. Existing files are preserved.", "warning");
    } catch (e) {
      if (dialog === ui.d) error.set({ label: e.message });
    }
    prepare.on("activate", async () => {
      if (dialog !== ui.d || !ui.d.open || prepare.disabled) return;
      if (!connected()) {
        error.set({ label: "Reconnect and refresh the exact installation before preparing removal." });
        return;
      }
      let q;
      try {
        if (!candidates.some((v) => v.provision_key === choice.value)) throw Error("Choose an exact original installation.");
        q = { key: crypto.randomUUID(), kind: "uninstall", uninstall: uninstallDraft(entry.host, choice.value, reason.value, mode.value, timeout.value) };
        if (!storage?.setItem || pendingStoreInvalid || pendingKeys.length >= 16) throw Error("Durable operation-key storage is unavailable. No uninstall preparation was sent.");
        const next = [.../* @__PURE__ */ new Set([...pendingKeys, q.key])];
        storage.setItem("arm-mesh-pending", JSON.stringify(next));
        pendingKeys = next;
      } catch (e) {
        error.set({ label: e.message });
        return;
      }
      preparing = true;
      prepare.disabled = true;
      try {
        const op = await call("mesh_operator_prepare", q);
        assertUninstallOperation(op, q.key, entry.host);
        const chosen = candidates.find((v) => v.provision_key === q.uninstall.provision_key);
        if (op.review && (op.review.stop.plan.target.home !== chosen.home || op.review.installation.sha256 !== chosen.sha256 || BigInt(op.review.installation.size_bytes) !== BigInt(chosen.size_bytes) || op.review.installation.os !== chosen.os || op.review.installation.arch !== chosen.arch || `${op.review.installation.install_root}/artifact-${chosen.sha256}/arm` !== chosen.binary)) throw Error("The uninstall review changed the selected original installation.");
        if (dialog === ui.d && ui.d.open) {
          ui.d.close();
          showOperation(op);
        } else {
          announce(`Uninstall preparation recorded as ${q.key}. Inspect that exact key.`);
          refresh();
        }
      } catch (e) {
        if (dialog === ui.d) error.set({ label: `${e.message}. Preparation may be recorded as ${q.key}. Inspect that exact key; it will not be resent.` });
        renderOperations();
      }
    });
  }
  async function freshStopReview(op) {
    const opener = dialog, version = ++lookupVersion;
    if (op.kind !== "stop" || op.can_prepare_fresh !== true || op.lookup_only === true || op.approved === true) return;
    const host = op.review?.plan?.target?.host;
    if (!validHostID(host)) {
      announce("The exact original target is unavailable. Inspect the immutable record.");
      return;
    }
    try {
      const entry = await call("node_inspect", { host });
      if (version !== lookupVersion || dialog !== opener || !opener?.open) return;
      if (entry.host !== host) throw Error("Fresh node identity differs");
      const available = nodeStopAvailability(entry);
      if (!available.enabled) throw Error(available.reason);
      editStop(entry);
    } catch (e) {
      announce(`${e.message}. The original Stop receipt is retained; no new request was sent.`);
    }
  }
  function editDisconnect(entry) {
    const ui = modal("Disconnect this node", "MESH / EXACT ENROLLMENT"), facts = graph.stack();
    row(facts, "Node", entry.name || entry.display_name || entry.host);
    row(facts, "Stable host", entry.host, true);
    row(facts, "Enrollment", entry.trust?.pair, true);
    ui.body.append(facts);
    note(ui.body, "Disconnect revokes this enrollment locally first and closes its sessions. An offline destination may need explicit recovery. Existing leases, expiry and cleanup obligations remain. Other nodes are unchanged.", "warning");
    const form = graph.stack(), reason = field(form, "Reason for disconnecting", { type: "textarea", required: true }), error = graph.text("", { role: "alert" });
    error.set({ role: "alert" });
    form.append(error);
    ui.body.append(form);
    const keep = button("Keep connection", () => ui.d.close()), prepare = button("Prepare disconnect review", () => {
    }, "danger");
    keep.localDismiss = true;
    ui.foot.append(keep, prepare);
    keep.focus();
    prepare.on("activate", async () => {
      if (disposed || dialog !== ui.d || !ui.d.open || prepare.disabled) return;
      if (!connected()) {
        error.set({ label: "Reconnect before preparing a disconnect." });
        return;
      }
      if (!reason.value.trim()) {
        error.set({ label: "Explain why this enrollment should be disconnected." });
        return;
      }
      const key = crypto.randomUUID();
      try {
        validateMeshReason(reason.value);
        remember(key);
      } catch (e) {
        error.set({ label: e.message });
        return;
      }
      prepare.disabled = true;
      try {
        const op = await call("mesh_operator_prepare", { key, kind: "disconnect", disconnect: { host: entry.host, pair: entry.trust.pair, reason: reason.value } });
        if (dialog === ui.d) {
          ui.d.close();
          showOperation(op);
        } else {
          announce(`Operation ${op.key} recorded. View its receipt under Mesh operations.`);
          refresh();
        }
      } catch (e) {
        error.set({ label: `${e.message}. Inspect operation ${key} before any retry.` });
        renderOperations();
      }
    });
  }
  function identityCard(label, identity, listen) {
    const box = graph.stack();
    box.append(graph.heading(label, 4));
    const facts = graph.stack();
    row(facts, "Name", identity.name);
    row(facts, "Stable host", identity.host, true);
    row(facts, "Run as", identity.owner_username ? identity.owner_uid ? `${identity.owner_username} (UID ${identity.owner_uid})` : identity.owner_username : identity.owner_uid);
    row(facts, "Advertised endpoint", identity.address, true);
    if (listen) row(facts, "Exact listener bind", listen, true);
    row(facts, "Certificate SHA-256", identity.certificate_sha256, true);
    row(facts, "Public key SHA-256", identity.public_key_sha256, true);
    row(facts, "Expires", when(identity.certificate_expires));
    box.append(facts);
    return box;
  }
  function semantic(op) {
    return stringifyJSON([provisionPending, provisionStoreInvalid, op.provision_connection_ready, uninstallPending, uninstallStoreInvalid, op.lookup_only, op.can_prepare_fresh, uncertainStopDecisions.has(op.key), stopStoreInvalid, op.key, op.phase, op.worker_live, op.cancel_requested, op.review_sha256, op.review_revision, op.approved, op.result, op.error, op.resolved_by, op.recovery_sha256, canReview(op)].map((value) => value ?? null));
  }
  function renderOperation(ui, op) {
    if (dialog !== ui.d || !ui.d.open) return;
    if (op.kind === "provision") {
      assertProvisionCurrent(op, op.key);
      if (currentOperation?.key === op.key && currentOperation.kind === "provision" && BigInt(op.revision) < BigInt(currentOperation.revision)) return;
      settleProvision(op);
    }
    if (op.kind === "uninstall") {
      assertUninstallOperation(op, op.key, currentOperation?.kind === "uninstall" && currentOperation.key === op.key ? uninstallOperationTarget(currentOperation) : null);
      if (currentOperation?.key === op.key && currentOperation.kind === "uninstall" && BigInt(op.revision) < BigInt(currentOperation.revision)) return;
      settleUninstall(op);
    }
    if (op.kind === "stop") {
      assertStopOperation(op, op.key, currentOperation?.kind === "stop" && currentOperation.key === op.key ? stopOperationTarget(currentOperation) : null);
      if (currentOperation?.key === op.key && currentOperation.kind === "stop" && BigInt(op.revision) < BigInt(currentOperation.revision)) return;
    }
    if (op.kind === "stop" && (op.lookup_only === true || op.can_prepare_fresh === true)) settleStopDecision(op.key);
    forget(op.key);
    currentOperation = op;
    renderedSemantic = semantic(op);
    renderedRevision = String(op.revision);
    ui.heading.set({ label: phaseLabel(op) });
    ui.defaultFocus = null;
    ui.body.replace();
    ui.foot.replace();
    const banner = graph.stack();
    banner.set({ role: "status" });
    banner.append(graph.text(op.kind === "provision" ? "INSTALL ARM" : op.kind === "connect" ? "CONNECT" : op.kind === "reconnect" ? "RECONNECT" : op.kind === "disconnect" ? "DISCONNECT" : op.kind === "stop" ? "STOP ARM" : op.kind === "uninstall" ? "UNINSTALL" : "OTHER OPERATION"), graph.text(operationAdvice(op)));
    ui.body.append(banner);
    const facts = graph.stack();
    row(facts, "Exact operation", op.key, true);
    row(facts, "Updated", when(op.updated_at));
    if (op.parent_key) row(facts, "Original intent", op.parent_key, true);
    ui.body.append(facts);
    if (op.error) {
      if (op.kind === "provision" && op.phase === "rejected" && op.error.code === "confirmation_declined") note(ui.body, "You kept this host unchanged. No installation was authorized.");
      else note(ui.body, `${op.error.code}: ${op.error.message}`, "warning");
    }
    const review = op.review;
    if (op.kind === "provision" && review) {
      ui.body.append(provisionReviewView(review));
      if (provisionStoreInvalid || provisionPending.decisions.includes(op.key)) note(ui.body, "An installation decision may already be recorded. Inspect this exact key; confirmation is disabled until its admitted or ended state is known.", "warning");
    } else if (op.kind === "uninstall" && review) {
      ui.body.append(uninstallReviewView(review));
      if (uninstallStoreInvalid || uninstallPending.decisions.includes(op.key) || pendingFinish(op.key)) note(ui.body, "An uninstall decision or finish may already be recorded. Inspect this exact key. No uncertain action is resent. A fresh native inspection is required before any separately confirmed finish.", "warning");
    } else if (op.kind === "stop" && review) {
      const snapshot = { key: op.key, review_revision: op.review_revision, review_sha256: op.review_sha256 };
      ui.body.append(stopReviewView(review, { inspectLease: async (item) => {
        const current = () => dialog === ui.d && ui.d.open && currentOperation?.key === snapshot.key && currentOperation?.review_revision === snapshot.review_revision && currentOperation?.review_sha256 === snapshot.review_sha256;
        if (!connected() || !current()) throw Error("Reconnect and refresh this exact Stop review before reading lease details.");
        const q = { ...snapshot, obligation: item.id }, detail2 = await call("mesh_operator_stop_lease", q);
        if (!current()) throw Error("This Stop review is no longer open.");
        if (detail2.key !== q.key || detail2.review_revision !== q.review_revision || detail2.review_sha256 !== q.review_sha256 || detail2.obligation !== item.id || detail2.id !== item.id.replace(/^lease:/, "") || detail2.host !== review.plan.target.host) throw Error("The response does not match this exact reviewed destination lease.");
        return detail2;
      } }));
      if (stopStoreInvalid || uncertainStopDecisions.has(op.key)) note(ui.body, "A Stop decision may already be recorded. Look up this exact operation; confirmation is disabled until the backend reports its admitted or safely ended state.", "warning");
    } else if (review) {
      if (!supportedReview(op)) note(ui.body, "This review type is not supported by this dashboard. Inspect it using the CLI; no approval is available here.", "warning");
      ui.body.append(graph.heading(supportedReview(op) ? "Concrete trust review" : "Recorded review", 3));
      const identities = graph.stack();
      for (const item of reviewIdentities(review)) identities.append(identityCard(item.label, item.identity, item.listen));
      ui.body.append(identities);
      if (review.ssh_identity || review.ssh_account) {
        const ssh = graph.stack();
        for (const [key, value] of Object.entries(review.ssh_identity ?? { account: review.ssh_account, host_key: review.ssh_host_key })) row(ssh, `SSH ${key.replaceAll("_", " ")}`, String(value), true);
        ui.body.append(ssh);
      }
      if (review.request || review.source_permissions_on_target) {
        const request = review.request ?? review;
        const grants = graph.stack();
        grants.append(grantCard("This node \u2192 destination", request.source_permissions_on_target), grantCard("Destination \u2192 this node", request.target_permissions_on_source));
        ui.body.append(grants);
      }
      if (review.reason ?? review.request?.reason) row(facts, "Reason", review.reason ?? review.request.reason);
      if (review.request?.reconnect) {
        const binding = review.request.reconnect.binding;
        row(facts, "Previous exact enrollment", binding?.previous_enrollment?.pair, true);
        row(facts, "Previous revision", binding?.previous_enrollment?.revision);
        row(facts, "Installed helper (path/version evidence)", review.request.remote_binary, true);
        row(facts, "Reported ARM home", review.remote?.home, true);
        row(facts, "Reported ARM version", review.remote?.version);
        note(ui.body, "The helper is reused without installation or upgrade. Historical executable byte equality is not established.");
      }
      if (review.operation === "disconnect-direct-peer") {
        row(facts, "Exact enrollment", review.pair, true);
        row(facts, "Local enrollment revision", review.local_revision);
        row(facts, "Destination enrollment revision", review.remote_revision);
      }
      note(ui.body, review.warning || review.effect || op.notice || meshPreparationNotice, "warning");
      if (canReview(op)) {
        note(ui.body, `This review expires ${when(op.review_expires_at)}. Confirmation binds these exact identities, endpoints, permissions and revisions.`);
      }
      ui.body.append(graph.json(review, "Full immutable review"));
    } else note(ui.body, op.notice || meshPreparationNotice);
    if (op.result) {
      if (op.kind === "uninstall") {
        ui.body.append(graph.heading(uninstallConfirmed(op) ? "Managed executable removal verified" : "Original uninstall outcome", 3));
        if (op.result.detail) note(ui.body, op.result.detail);
        if (!uninstallConfirmed(op)) note(ui.body, "No complete removal is inferred from a disconnected backend or an absent path. Inspect the exact retained receipt.", "warning");
      }
      if (op.kind === "stop") {
        ui.body.append(graph.heading(stopResultLabel(op.result, op.key, op.review ?? {}), 3));
        if (op.result.status === "stopped" && !stopResultConfirmed(op.result, op.key, op.review ?? {})) note(ui.body, "The recorded response lacks positive original-generation completion. Treat the outcome as unknown and look up the exact receipt.", "warning");
        if (op.result.detail) note(ui.body, op.result.detail);
      }
      ui.body.append(graph.json(op.result, "Recorded result"));
    }
    const close = button("Close", () => ui.d.close());
    ui.foot.append(close);
    if (declineAllowed(op)) {
      close.set({ label: "Keep review open later" });
      const decline = button(op.kind === "provision" ? "Keep host unchanged" : op.kind === "uninstall" ? "Keep installed" : op.kind === "stop" ? "Keep running" : "Decline this review", () => decide(false), "mesh-decline"), approve = reviewAllowed(op) ? button(op.kind === "provision" ? "Install reviewed ARM node" : op.kind === "uninstall" ? "Approve managed uninstall" : op.kind === "stop" ? "Stop this ARM backend" : op.kind === "disconnect" ? "Confirm disconnect" : op.kind === "reconnect" ? "Trust and reconnect" : "Trust and connect", () => decide(true), ["disconnect", "stop", "uninstall"].includes(op.kind) ? "danger" : "primary") : null;
      ui.foot.append(decline);
      ui.defaultFocus = decline;
      if (approve) ui.foot.append(approve);
      async function decide(approved) {
        if (["uninstall", "provision"].includes(op.kind) && (dialog !== ui.d || !ui.d.open || currentOperation?.key !== op.key || currentOperation?.review_revision !== op.review_revision || currentOperation?.review_sha256 !== op.review_sha256)) return;
        if (!connected()) {
          note(ui.body, "Reconnect and refresh this review before deciding.", "warning");
          return;
        }
        if (!(approved ? reviewAllowed(op) : declineAllowed(op))) {
          note(ui.body, "This review cannot be decided now. Read its exact receipt before any new action.", "warning");
          return;
        }
        try {
          if (op.kind === "provision") {
            retainProvision("decisions", op.key);
            opVersion++;
          }
          if (op.kind === "stop") {
            retainStopDecision(op.key);
            opVersion++;
          }
          if (op.kind === "uninstall") {
            retainUninstall("decisions", op.key);
            opVersion++;
          }
        } catch (e) {
          note(ui.body, e.message, "warning");
          return;
        }
        decline.disabled = true;
        if (approve) approve.disabled = true;
        try {
          const next = await call("mesh_operator_decide", { key: op.key, review_revision: op.review_revision, review_sha256: op.review_sha256, approve: approved });
          if (op.kind === "stop") {
            assertStopOperation(next, op.key, stopOperationTarget(op));
            if (next.review_revision !== op.review_revision || next.review_sha256 !== op.review_sha256) throw Error("The Stop decision response changed the exact reviewed revision or hash. Retain the key and inspect it.");
            if (["preparing", "awaiting_review"].includes(next.phase) && !next.lookup_only && !next.can_prepare_fresh) throw Error("The Stop response has not confirmed the decision transition. Retain its exact key and inspect the receipt.");
          }
          if (op.kind === "provision") {
            assertProvisionOperation(next, op.key, provisionReference(op));
            if (BigInt(next.revision) < BigInt(op.revision) || ["preparing", "awaiting_review"].includes(next.phase) && !next.approved && !next.lookup_only) throw Error("The installation response has not confirmed this decision. Retain its exact key.");
            settleProvision(next);
          }
          if (op.kind === "uninstall") {
            assertUninstallOperation(next, op.key, uninstallOperationTarget(op), { plan: op.review, hash: op.review_sha256, revision: op.review_revision });
            if (["preparing", "awaiting_review"].includes(next.phase) && !next.lookup_only && !["rejected", "declined"].includes(next.phase)) throw Error("The uninstall response has not confirmed this decision. Retain its exact key.");
            settleUninstall(next);
          }
          if (op.kind === "stop") settleStopDecision(op.key);
          renderOperation(ui, next);
          onChanged();
        } catch (e) {
          note(ui.body, `${e.message}. The decision may have been recorded. Refresh this exact receipt; no decision will be resent.`, "warning");
          renderOperations();
          ui.foot.append(button("Look up recorded outcome", () => lookup(op.key)));
        }
      }
    } else if (!["uninstall", "provision"].includes(op.kind) && op.worker_live && !op.cancel_requested && !(op.kind === "stop" && (op.lookup_only || op.approved || uncertainStopDecisions.has(op.key) || stopStoreInvalid))) {
      ui.foot.append(button("Request cancellation", async () => {
        if (!connected()) return;
        try {
          renderOperation(ui, await call("mesh_operator_cancel", { key: op.key, revision: currentOperation?.key === op.key ? currentOperation.revision : op.revision }));
        } catch (e) {
          note(ui.body, `${e.message}. Inspect this exact operation before another action.`, "warning");
        }
      }));
    }
    if (op.kind === "provision") {
      ui.foot.append(button("Refresh recorded receipt", () => lookup(op.key)));
      if (op.lookup_only || op.approved) ui.foot.append(button("Inspect original installation", () => lookupProvision(op)));
      if (provisionConfirmed(op)) {
        const conn = op.review.request.connection_key;
        ui.foot.append(button(pendingKeys.includes(conn) || allOps.some((v) => v.key === conn) ? "Inspect mesh connection" : "Review mesh connection\u2026", () => pendingKeys.includes(conn) || allOps.some((v) => v.key === conn) ? lookup(conn) : editConnect(null, op), "primary"));
      }
    }
    if (op.kind === "stop") {
      ui.foot.append(button("Refresh recorded receipt", () => lookup(op.key)));
      if (op.lookup_only || op.approved) ui.foot.append(button("Check original stop", () => lookupStop(op)));
      if (op.can_prepare_fresh === true && !op.lookup_only && !op.approved) ui.foot.append(button("Prepare a fresh stop review\u2026", () => freshStopReview(op)));
    }
    if (op.kind === "uninstall") {
      ui.foot.append(button("Refresh recorded receipt", () => lookup(op.key)));
      if (op.lookup_only || op.approved) ui.foot.append(button("Inspect original uninstall", () => lookupUninstall(op)));
      if (finishReady(op) && !uninstallStoreInvalid && !pendingFinish(op.key)) ui.foot.append(button("Review finishing removal\u2026", () => finishUninstall(op), "danger"));
    }
    if (!["stop", "uninstall", "provision"].includes(op.kind) && op.phase === "incomplete" && !op.worker_live && !op.resolved_by) {
      if (resumeStoreInvalid || uncertainResumes.has(op.key)) {
        note(ui.body, "A recovery request may already have created a child attempt. Inspect Mesh operations and this exact parent; this request cannot be resent here. Separately keyed recovery lookup is still required for in-app resolution.", "warning");
      } else {
        const resume = button("Prepare recovery review\u2026", async () => {
          if (!connected()) return;
          resume.disabled = true;
          try {
            retainResume(op.key);
          } catch (e) {
            note(ui.body, e.message, "warning");
            return;
          }
          try {
            const next = await call("mesh_operator_resume", { key: op.key, revision: op.revision });
            settleResume(op.key);
            forget(next.key);
            opKey = next.key;
            if (dialog === ui.d && ui.d.open) renderOperation(ui, next);
            else refresh();
          } catch (e) {
            note(ui.body, `${e.message}. Recovery may already have created a child attempt. Inspect this parent and Mesh operations; no request will be resent.`, "warning");
          }
        });
        ui.foot.append(resume);
      }
    }
    if (!["stop", "uninstall", "provision"].includes(op.kind) && op.recovery_sha256 && !op.worker_live && !op.resolved_by) ui.foot.append(button("Hand off unresolved operation\u2026", () => editHandoff(op), "danger"));
    (ui.defaultFocus ?? close).focus();
  }
  function editHandoff(op) {
    const ui = modal("Hand off unresolved operation", "MESH / WORKFLOW EXCLUSION"), facts = graph.stack();
    row(facts, "Exact operation", op.key, true);
    row(facts, "Recorded phase", phaseLabel(op));
    row(facts, "Operation revision", String(op.revision));
    ui.body.append(facts);
    const fingerprint = graph.disclosure("Handoff review fingerprint \xB7 SHA-256"), full = graph.code(op.recovery_sha256);
    full.set({ help: "Full handoff review SHA-256 fingerprint" });
    fingerprint.append(graph.text("Handoff review fingerprint \xB7 SHA-256"), graph.text("This fingerprint binds the exact operation snapshot being handed off. A changed operation requires a fresh review."), full);
    ui.body.append(fingerprint);
    note(ui.body, op.recovery_effect || "Release only this workflow exclusion. This does not establish remote cleanup, revoke trust, close listeners or release any lease.", "warning");
    note(ui.body, "Inspect the recorded review and result before proceeding. Other operators may be able to prepare a new change after handoff; unresolved resource and trust obligations remain.");
    const form = graph.stack(), reason = field(form, "Reason and operator recovery plan", { type: "textarea", required: true }), error = graph.text("", { role: "alert" });
    form.append(error);
    ui.body.append(form);
    const keep = button("Keep operation protected", () => ui.d.close()), confirm = button("Confirm workflow handoff", () => {
    }, "danger");
    ui.foot.append(keep, confirm);
    keep.focus();
    confirm.on("activate", async () => {
      if (disposed || dialog !== ui.d || !ui.d.open || confirm.disabled) return;
      if (!connected() || !reason.value.trim()) {
        error.set({ label: "A live connection and meaningful recovery reason are required." });
        return;
      }
      try {
        validateMeshReason(reason.value);
      } catch (e) {
        error.set({ label: e.message });
        return;
      }
      confirm.disabled = true;
      try {
        const result = await call("mesh_operator_handoff", { key: op.key, revision: op.revision, review_sha256: op.recovery_sha256, reason: reason.value });
        if (dialog === ui.d) {
          ui.d.close();
          showOperation(result);
        } else refresh();
      } catch (e) {
        error.set({ label: `${e.message}. Inspect the exact operation receipt before another attempt.` });
      }
    });
  }
  function showOperation(op) {
    if (op.kind === "provision") assertProvisionCurrent(op, op.key);
    if (["stop", "uninstall", "provision"].includes(op.kind) && currentOperation?.kind === op.kind && op.key === currentOperation.key && BigInt(op.revision) < BigInt(currentOperation.revision)) return;
    const ui = modal(phaseLabel(op), "MESH / DURABLE OPERATION");
    opKey = op.key;
    forget(op.key);
    renderOperation(ui, op);
  }
  async function lookup(key) {
    const opener = dialog, version = ++lookupVersion;
    try {
      const op = await call("mesh_operator_get", { key });
      if (version !== lookupVersion || dialog !== opener) return;
      if (op.key !== key) throw Error("Recorded operation key differs");
      if (op.kind === "provision" || provisionPending.prepares.some((q) => q.key === key) || provisionPending.decisions.includes(key) || currentOperation?.kind === "provision" && currentOperation.key === key) assertProvisionCurrent(op, key);
      if (op.kind === "stop" || uncertainStopDecisions.has(key) || currentOperation?.kind === "stop" && currentOperation.key === key) assertStopOperation(op, key, currentOperation?.kind === "stop" && currentOperation.key === key ? stopOperationTarget(currentOperation) : null);
      if (op.kind === "uninstall" || uninstallPending.decisions.includes(key) || pendingFinish(key) || currentOperation?.kind === "uninstall" && currentOperation.key === key) assertUninstallOperation(op, key, currentOperation?.kind === "uninstall" && currentOperation.key === key ? uninstallOperationTarget(currentOperation) : null, currentOperation?.kind === "uninstall" && currentOperation.key === key && currentOperation.review ? { plan: currentOperation.review, hash: currentOperation.review_sha256, revision: currentOperation.review_revision, provisional: currentOperation.phase === "preparing" && !currentOperation.approved && !currentOperation.lookup_only } : null);
      showOperation(op);
    } catch (e) {
      if (version === lookupVersion && dialog === opener) announce(`${e.message}. Operation ${key} was not retried.`);
    }
  }
  async function lookupStop(op) {
    if (op.kind !== "stop" || !op.lookup_only && !op.approved || !connected()) return;
    const opener = dialog, version = ++lookupVersion;
    opVersion++;
    try {
      const next = await call("mesh_operator_stop_lookup", { key: op.key, revision: op.revision });
      if (version !== lookupVersion || dialog !== opener || !opener?.open) return;
      assertStopOperation(next, op.key, stopOperationTarget(op));
      renderOperation(modalParts.get(opener), next);
    } catch (e) {
      announce(`${e.message}. The stop was not resent. Refresh the exact source receipt before another explicit check.`);
    }
  }
  async function lookupUninstall(op) {
    if (op.kind !== "uninstall" || !op.lookup_only && !op.approved || !connected()) return;
    const opener = dialog, version = ++lookupVersion;
    opVersion++;
    try {
      const next = await call("mesh_operator_uninstall_lookup", { key: op.key, revision: op.revision });
      if (version !== lookupVersion || dialog !== opener || !opener?.open) return;
      assertUninstallOperation(next, op.key, uninstallOperationTarget(op), { plan: op.review, hash: op.review_sha256, revision: op.review_revision });
      if (BigInt(next.revision) < BigInt(op.revision)) throw Error("The original uninstall lookup is older than this receipt.");
      if (uninstallFinishAllowed(next)) uninstallLookupReady.set(next.key, stringifyJSON([next.revision, next.review_revision, next.review_sha256]));
      else uninstallLookupReady.delete(next.key);
      settleUninstall(next, true);
      renderOperation(modalParts.get(opener), next);
    } catch (e) {
      if (version === lookupVersion && dialog === opener && opener?.open) announce(`${e.message}. Only the original uninstall was inspected; Stop, copying and removal were not resent.`);
    }
  }
  async function lookupProvision(op) {
    if (op.kind !== "provision" || !op.lookup_only && !op.approved || !connected()) return;
    const opener = dialog, version = ++lookupVersion;
    opVersion++;
    try {
      const next = await call("mesh_operator_provision_lookup", { key: op.key, revision: op.revision });
      if (version !== lookupVersion || dialog !== opener || !opener?.open) return;
      assertProvisionOperation(next, op.key, provisionReference(op));
      if (BigInt(next.revision) < BigInt(op.revision)) throw Error("The original installation lookup is older than this receipt.");
      settleProvision(next);
      renderOperation(modalParts.get(opener), next);
    } catch (e) {
      if (version === lookupVersion && dialog === opener && opener?.open) announce(`${e.message}. Only the original installation was inspected; upload, startup and catalog transfer were not repeated.`);
    }
  }
  function finishUninstall(op) {
    if (!connected() || !finishReady(op) || uninstallStoreInvalid || pendingFinish(op.key)) return;
    const snapshot = structuredClone(op), ui = modal("Finish the original removal?", "MESH / EXACT UNINSTALL FINISH");
    ui.body.append(uninstallReviewView(snapshot.review));
    note(ui.body, "This is an explicit finish of the retained original key. It never retries Stop or copies a new helper. The destination must freshly prove the original stopped generation, current permissions and exact owned executable; uncertain duties remain retained.", "warning");
    const facts = graph.stack();
    row(facts, "Original operation", snapshot.key, true);
    row(facts, "Source revision", snapshot.revision);
    ui.body.append(facts);
    const keep = button("Keep unchanged", () => ui.d.close()), finish = button("Finish exact removal", async () => {
      if (dialog !== ui.d || !ui.d.open || finish.disabled || !connected()) return;
      try {
        retainUninstall("finishes", snapshot.key, snapshot);
      } catch (e) {
        note(ui.body, e.message, "warning");
        return;
      }
      finish.disabled = true;
      const version = ++lookupVersion;
      opVersion++;
      try {
        const next = await call("mesh_operator_uninstall_finish", { key: snapshot.key, revision: snapshot.revision, review_sha256: snapshot.review_sha256 });
        if (version !== lookupVersion || dialog !== ui.d || !ui.d.open) return;
        assertUninstallOperation(next, snapshot.key, uninstallOperationTarget(snapshot), { plan: snapshot.review, hash: snapshot.review_sha256, revision: snapshot.review_revision });
        settleUninstall(next);
        showOperation(next);
      } catch (e) {
        if (version === lookupVersion && dialog === ui.d && ui.d.open) {
          note(ui.body, `${e.message}. This finish may already have effects. Inspect the exact key before considering another separately confirmed finish.`, "warning");
          ui.foot.append(button("Inspect original uninstall", () => lookupUninstall(snapshot)));
        }
      }
    }, "danger");
    keep.localDismiss = true;
    ui.foot.append(keep, finish);
    keep.focus();
  }
  function provisionReviewView(review) {
    if (!supportedProvisionReview(review)) return graph.card("Exact ARM installation", [
      graph.text("Exact supported installation review unavailable. Keep this host unchanged and inspect the original receipt.", { tone: "Warning" }),
      graph.json(review, "Unsupported installation review")
    ]);
    const q = review.request, t = q.transfer;
    const destination = graph.stack(), software = graph.stack();
    for (const [label, value] of [["SSH destination", q.ssh_destination], ["Run as", `${q.ssh_identity?.account ?? "Unknown"} (UID ${t.expected_uid ?? "Unknown"})`], ["ARM home", q.remote_home], ["Private executable", review.final_binary]]) row(destination, label, value);
    for (const [label, value] of [["Platform", `${t.os} / ${t.arch}`], ["ARM version", t.expected_arm_version], ["Image size", bytes(t.size_bytes)], ["Local source", "Trusted ARM companion"]]) row(software, label, value);
    const boundary = graph.card("Two separate decisions", [
      graph.text("1. Install and start the reviewed user-level ARM node."),
      graph.text("2. Review the node identity and choose mesh permissions. Installation grants no peer access.")
    ]);
    const content = [graph.text("Installation and mesh trust are separate decisions.", { tone: "Warning" }), graph.card("Destination", [destination]), graph.card("Verified software", [software]), boundary];
    if (q.seed_catalog) content.push(graph.card("Companions for future nodes", [
      graph.text("The four reviewed compressed companions are retained for onward provisioning. They grant no mesh access."),
      ...(q.seed_catalog.manifest?.artifacts ?? []).map((a) => graph.text(`${a.os} / ${a.arch} \xB7 ${bytes(a.compressed_size_bytes)} compressed \xB7 ${bytes(a.size_bytes)} executable`))
    ]));
    const resources = q.startup.resources;
    content.push(graph.disclosure("SSH pin, image digest and startup settings", [
      graph.code(`SSH host key: ${q.ssh_identity.host_key}
Executable SHA-256: ${t.sha256}
Local source: ${review.artifact_source}
Local listener: ${q.startup.listen}
CPU budget: ${resources.cpu}
Memory ceiling: ${BigInt(resources.memory_bytes) > 0n ? bytes(resources.memory_bytes) : "Current host availability"}
Disk ceiling: ${BigInt(resources.disk_bytes) > 0n ? bytes(resources.disk_bytes) : "Current host availability"}`)
    ]), graph.heading("Operator reason", 4), graph.text(t.reason), graph.text(review.effect), graph.json(review, "Full immutable installation and startup review"));
    return graph.card("Install ARM for this user", content);
  }
  function uninstallReviewView(review) {
    const s = review?.stop?.plan, q = review?.installation;
    if (!s || !q) return graph.text("The exact installation review is unavailable. Approval is disabled.", { tone: "Warning" });
    const installation = graph.stack(), authority = graph.stack(), work = graph.stack([graph.heading("Work and connections affected", 4)]);
    for (const [label, value] of [["Stable host", s.target?.host], ["Run as", review.stop.owner_username ? `${review.stop.owner_username} (UID ${s.target?.owner})` : `UID ${s.target?.owner}`], ["ARM home", s.target?.home], ["Managed executable", `${q.install_root}/artifact-${q.sha256}/arm`], ["Platform", `${q.os} / ${q.arch}`], ["Binary size", bytes(q.size_bytes)], ["Original transfer", q.key]]) row(installation, label, value);
    for (const [label, value] of [["Source host", s.actor?.source_host], ["Enrollment", s.actor?.pair], ["Grant revision", s.actor?.grant_revision], ["Access required", "Both node-stop and node-uninstall"], ["Backend generation", s.target?.backend_generation], ["Stop policy", stopPolicyLabel(s.policy)]]) row(authority, label, value);
    work.append(graph.text(`${s.impact?.outstanding ?? "Unknown"} outstanding obligations \xB7 ${s.impact?.affected_peers?.length ?? 0} affected peers`));
    for (const item of s.impact?.obligations ?? []) {
      const itemView = graph.card(item.id, [graph.text(`${item.kind} \xB7 ${item.state} \xB7 revision ${item.revision}`)]);
      if (item.lease_details === "permitted" && item.lease) itemView.append(graph.text(`${item.lease.owner_username ?? "UID " + item.lease.owner} \xB7 ${item.lease.reason || "Purpose not shared in preview"}`));
      work.append(itemView);
    }
    for (const blocker of s.impact?.blockers ?? []) work.append(graph.text(blocker.detail, { tone: "Warning" }));
    const content = [
      graph.text("Stop the reviewed ARM backend, then remove one owned executable. Keep all retained data.", { tone: "Warning" }),
      graph.card("Exact installation", [installation]),
      graph.card("Separate authority", [authority]),
      graph.card("What stays", [graph.text("Uninstall removes the reviewed executable only. It does not purge the ARM home or stop the host."), ...(review.preserved ?? []).map((name) => graph.text(name))]),
      graph.disclosure("Retained recovery helper and executable identity", [graph.code(`${review.retained_recovery_helper}
Executable SHA-256: ${q.sha256}`)]),
      work,
      graph.heading("Operator reason", 4),
      graph.text(review.reason),
      graph.text(`Review expires ${when(s.expires_at)}. A changed review requires a new decision.`)
    ];
    if (!supportedUninstallReview(review)) content.push(graph.text("This installation, work census or removal policy cannot currently be approved. Keep it installed and inspect the exact receipt.", { tone: "Warning" }));
    content.push(graph.json(review, "Full immutable installation, ownership and Stop review"));
    return graph.card("Remove this managed ARM executable", content);
  }
  function stopReviewView(review, { inspectLease } = {}) {
    const p = review?.plan;
    if (!p) return graph.text("The exact stop plan is unavailable. Refresh its receipt; approval is unavailable.", { tone: "Warning" });
    const identity = graph.stack();
    row(identity, "Stable host", p.target?.host);
    row(identity, "Run as", review.owner_username ? `${review.owner_username} (UID ${p.target?.owner})` : `UID ${p.target?.owner ?? "not reported"} \xB7 name not included in review`);
    row(identity, "ARM home", p.target?.home);
    row(identity, "Backend generation", p.target?.backend_generation);
    row(identity, "Desired-state revision", p.desired_state_revision?.toString());
    row(identity, "Source host", p.actor?.source_host);
    row(identity, "Exact enrollment", p.actor?.pair);
    row(identity, "Grant revision", p.actor?.grant_revision?.toString());
    const work = graph.stack([graph.heading("Affected work and connections", 4)]);
    if (p.impact?.complete !== true) work.append(graph.text("The work census is incomplete. It cannot be treated as an empty backend.", { tone: "Warning" }));
    else work.append(graph.text(`${p.impact.outstanding} outstanding obligations \xB7 ${p.impact.affected_peers?.length ?? 0} affected peers`));
    for (const item of (p.impact?.obligations ?? []).slice(0, 64)) {
      const v = item.lease;
      const attribution = item.kind === "lease" && item.lease_details === "permitted" && v ? `${v.owner_username ? `${v.owner_username} (UID ${v.owner})` : `UID ${v.owner} \xB7 name not reported`} \xB7 ${v.reason || (v.truncated ? "Purpose omitted from this preview for size." : "Purpose not recorded.")}` : item.kind === "lease" ? "Lease details are not permitted or not included in this review." : item.reason ?? "Purpose not reported";
      const card = graph.card(item.id, [graph.text(`${item.kind} \xB7 ${item.state ?? "State not reported"} \xB7 revision ${item.revision ?? "not reported"}`), graph.text(attribution)]);
      if (v?.agent || v?.project) card.append(graph.text([v.agent && `Agent ${v.agent}`, v.project && `Project ${v.project}`].filter(Boolean).join(" \xB7 ")));
      if (item.kind === "lease" && item.lease_details === "permitted" && v && inspectLease) {
        const result = graph.stack();
        card.append(button("Inspect reviewed lease " + item.id, async () => {
          try {
            const detail2 = await inspectLease(item);
            result.replace(graph.json(detail2, "Complete reviewed destination lease"));
          } catch (error) {
            result.replace(graph.text(error.message, { tone: "Warning" }));
          }
        }), result);
      }
      work.append(card);
    }
    const content = [
      graph.text("Stops this ARM backend and its connections. The host remains running.", { tone: "Warning" }),
      identity,
      graph.text(stopPolicyLabel(p.policy)),
      graph.text(p.supervisor_scope || "Supervisor scope is not reported."),
      graph.text(review.completion_check?.detail || "Completion check not described."),
      work,
      graph.text(`Operator reason: ${p.reason ?? "Not reported"}`),
      graph.text(`Review expires ${when(p.expires_at)}. A changed review requires a new decision.`)
    ];
    if (stopReviewIssue(review)) content.push(graph.text(stopReviewIssue(review), { tone: "Warning" }));
    content.push(graph.json(review, "Full immutable Stop plan, identities and work census"));
    return graph.card("Stop ARM on this node", content);
  }
  function renderOperations() {
    const key = stringifyJSON([pendingKeys, provisionPending, provisionStoreInvalid, uninstallPending, uninstallStoreInvalid, [...uncertainStopDecisions], stopStoreInvalid, allOps.map((op) => [op.key, op.phase, op.cancel_requested, op.resolved_by, op.error, op.result, op.lookup_only, op.can_prepare_fresh, op.provision_connection_ready].map((v) => v ?? null))]);
    if (key === operationsKey) return;
    operationsKey = key;
    operations.replace();
    const head = graph.stack();
    head.append(graph.stack([graph.text("")]), graph.heading("Mesh operations", 3));
    head.replace(graph.heading("Mesh operations", 3), graph.text("Durable receipts \xB7 no replay"));
    operations.append(head);
    const entries = [...allOps].sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at));
    for (const key2 of [...pendingKeys]) {
      if (entries.some((op) => op.key === key2)) forget(key2);
      else operations.append(button(`Look up uncertain operation \xB7 ${key2}`, () => lookup(key2), "mesh-pending-lookup"));
    }
    for (const key2 of uncertainStopDecisions) if (!entries.some((op) => op.key === key2)) operations.append(button(`Look up uncertain Stop decision \xB7 ${key2}`, () => lookup(key2), "mesh-pending-lookup"));
    for (const key2 of /* @__PURE__ */ new Set([...uninstallPending.decisions, ...uninstallPending.finishes.map((v) => v.key)])) if (!entries.some((op) => op.key === key2)) operations.append(button(`Inspect uncertain uninstall \xB7 ${key2}`, () => lookup(key2), "mesh-pending-lookup"));
    for (const key2 of /* @__PURE__ */ new Set([...provisionPending.prepares.map((q) => q.key), ...provisionPending.decisions])) if (!entries.some((op) => op.key === key2)) operations.append(button(`Inspect uncertain installation \xB7 ${key2}`, () => lookup(key2), "mesh-pending-lookup"));
    if (provisionStoreInvalid) note(operations, "Saved installation recovery data is unreadable. Installation preparation and confirmation are disabled; inspect durable receipts before changing browser recovery data.", "warning");
    if (uninstallStoreInvalid) note(operations, "Saved uninstall recovery data is unreadable. Uninstall confirmation and finishing are disabled; inspect durable receipts before changing browser recovery data.", "warning");
    if (stopStoreInvalid) note(operations, "Saved Stop-decision recovery data is unreadable. Stop confirmation is disabled; inspect durable Mesh operations before changing browser recovery data.", "warning");
    if (!entries.length) {
      operations.append(graph.text("Connection and ARM lifecycle operations will appear here, including interrupted or incomplete work."));
      return;
    }
    operations.append(graph.stack(entries.map((op) => button(`${phaseLabel(op)} \xB7 ${op.key} \xB7 ${when(op.updated_at)} \xB7 View receipt`, () => lookup(op.key)))));
  }
  async function refresh(inspectSelected = false) {
    if (disposed || !connected()) return;
    if (refreshBusy) {
      refreshQueued = true;
      return;
    }
    for (const item of controls) if (!item.isConnected) controls.delete(item);
    refreshBusy = true;
    refreshButton.disabled = true;
    try {
      const next = await call("node_list");
      if (disposed) return;
      roster = next;
      renderRoster();
      allOps = await call("mesh_operator_list") ?? [];
      if (disposed) return;
      renderOperations();
      if (opKey && dialog?.open) {
        const key = opKey, version = ++opVersion, op = await call("mesh_operator_get", { key });
        if (opKey === key && version === opVersion && (op.kind === "provision" || currentOperation?.kind === "provision")) assertProvisionCurrent(op, key);
        if (opKey === key && version === opVersion && (op.kind === "stop" || currentOperation?.kind === "stop")) assertStopOperation(op, key, currentOperation?.kind === "stop" ? stopOperationTarget(currentOperation) : null);
        if (opKey === key && version === opVersion && (op.kind === "uninstall" || currentOperation?.kind === "uninstall")) assertUninstallOperation(op, key, currentOperation?.kind === "uninstall" ? uninstallOperationTarget(currentOperation) : null, currentOperation?.kind === "uninstall" && currentOperation.review ? { plan: currentOperation.review, hash: currentOperation.review_sha256, revision: currentOperation.review_revision, provisional: currentOperation.phase === "preparing" && !currentOperation.approved && !currentOperation.lookup_only } : null);
        if (opKey === key && version === opVersion && !(["stop", "uninstall", "provision"].includes(op.kind) && currentOperation?.key === op.key && BigInt(op.revision) < BigInt(currentOperation.revision))) currentOperation = op;
        if (opKey === key && version === opVersion && dialog?.open && semantic(op) !== renderedSemantic) {
          const active = graph.activeComponent;
          renderOperation(modalParts.get(dialog), op);
          if (active?.isConnected) active.focus();
        }
      }
      if (inspectSelected && selectedHost && detail?.d === dialog) {
        const host = selectedHost, entry = await call("node_inspect", { host });
        if (!disposed && detail?.d === dialog && selectedHost === host) {
          if (entry?.host !== host) throw Error("Node inspection differs from the exact requested host.");
          renderNode(detail, entry);
        }
      }
      announce("Registry refreshed. Peer metadata is fetched only when you inspect a node.");
    } catch (e) {
      announce(`${e.message}. Existing data may be stale; no operation was replayed.`);
    } finally {
      refreshBusy = false;
      refreshButton.disabled = !connected();
      if (refreshQueued) {
        refreshQueued = false;
        queueMicrotask(() => refresh());
      }
    }
  }
  renderOperations();
  return { refresh, invalidate: () => refresh(), openKubernetesInventory: () => kube.open(), focusKubernetesNode: reference => kube.focusKubernetesNode(reference), updateConnection() {
    installButton.disabled = connectButton.disabled = refreshButton.disabled = forwardButton.disabled = !connected();
    forwardUI?.updateConnection();
    for (const parts of modalParts.values()) parts.availability?.updateConnection();
    kube.updateConnection();
    if (!connected()) {
      announce("Disconnected. Cached nodes and receipts may be stale. Reconnect performs reads only.");
      if (dialog?.open) {
        for (const control of controls) if (control.isConnected && !control.localDismiss && !["Close", "Close \xD7", "Keep review open later", "Keep connection", "Cancel"].includes(control.label)) control.disabled = true;
      }
    } else {
      renderedSemantic = "";
    }
  }, dispose() {
    disposed = true;
    dialog?.close();
    forwardUI?.dispose();
    kube.dispose();
    root.replace();
  } };
}
function createKubeInventory({ ui, root, call, connected, onKubeInventory = () => {} }) {
  let opened = false, disposed = false, sequence = 0, timer = null, selection = null, choices = [], opening = null;
  let snapshot = null, snapshotRequest = null, snapshotVersion = 0;
  const cardByUID = new Map();
  const status = ui.text("", { role: "status" }), cards = ui.grid([], { columns: 2 }), selected = ui.stack();
  const clusterDetails = ui.disclosure("Cluster identity", [], { hidden: true });
  const context = ui.select("Context", [], { value: "" });
  const namespace = ui.field("Namespace", { placeholder: "Context namespace" });
  const query = ui.field("Find a Node", { placeholder: "Type a Node name, architecture, zone or instance type", maxLength: 128 });
  const content = ui.stack([ui.stack([context, namespace, query]), status, clusterDetails, cards, selected], { hidden: true });
  const open = ui.button("Browse Kubernetes Nodes", () => opened ? closeInventory() : openInventory());
  const inventoryCard = ui.card("Available Kubernetes Nodes", [
    ui.text("Inspect schedulable resources before preparing a separate reviewed Pod plan."), open, content
  ]);
  root.append(inventoryCard);
  function clear() {
    selection = null; snapshot = null; snapshotRequest = null; cardByUID.clear();
    selected.replace(); clusterDetails.hidden = true; clusterDetails.replace();
    onKubeInventory(null);
  }
  function current(request, version) {
    return !disposed && version === sequence && opened && connected() && context.value === request.context &&
      namespace.value.trim() === request.namespace && query.value.trim() === request.query;
  }
  function closeInventory() {
    opened = false; ++sequence; clearTimeout(timer); timer = null;
    clear(); cards.replace(); content.hidden = true; open.set({ label: "Browse Kubernetes Nodes" });
  }
  async function loadContexts(version) {
    status.set({ label: "Loading Kubernetes contexts…" });
    try {
      const loaded = await call("kube_contexts");
      if (disposed || version !== sequence || !opened || !connected()) return false;
      choices = Array.isArray(loaded) ? loaded : [];
      context.set({ options: choices.map(choice => ({ value: choice.name, label: `${choice.name} · ${choice.cluster_name}` })) });
      const preferred = choices.find(choice => choice.current) ?? choices[0];
      if (!preferred) { status.set({ label: "No Kubernetes contexts are available." }); return false; }
      context.value = preferred.name; namespace.value = preferred.namespace ?? "";
      return await search();
    } catch (error) {
      if (!disposed && version === sequence && opened) status.set({ label: `Kubernetes contexts unavailable: ${error.message}` });
      return false;
    }
  }
  async function openInventory() {
    if (disposed) return false;
    if (opened && opening) return opening;
    if (opened && snapshot && current(snapshotRequest, snapshotVersion)) return true;
    if (opened && context.value && choices.length) return search();
    opened = true; const version = ++sequence;
    clearTimeout(timer); timer = null; clear(); cards.replace(); content.hidden = false;
    open.set({ label: "Hide Kubernetes Nodes" });
    if (!connected()) { status.set({ label: "Connect to the local backend to browse Kubernetes Nodes." }); return false; }
    const pending = loadContexts(version); opening = pending;
    try { return await pending; } finally { if (opening === pending) opening = null; }
  }
  const raw = value => value === undefined || value === null || value === "" ? "Not reported" : String(value);
  const primary = node => [
    ["CPU allocatable", formatKubeQuantity(node.allocatable_cpu, "cpu")],
    ["RAM allocatable", formatKubeQuantity(node.allocatable_memory)],
    ["Disk allocatable", formatKubeQuantity(node.allocatable_ephemeral)],
    ["Architecture", `${node.os ?? "?"} / ${node.arch ?? "?"}`]
  ];
  function technical(node) {
    const quantities = []; let omitted = 0;
    for (const [kind, values] of [["Capacity", node.capacity], ["Allocatable", node.allocatable]]) {
      for (const [name, value] of Object.entries(values ?? {})) {
        if (quantities.length < 64) quantities.push([kind + " · " + name, raw(value)]);
        else omitted++;
      }
    }
    const facts = [["Exact UID", raw(node.uid)], ["Ready", node.ready ? "Yes" : "No"], ["Cordoned", node.unschedulable ? "Yes" : "No"],
      ["Zone", raw(node.zone)], ["Instance type", raw(node.instance_type)],
      ["CPU allocatable (raw)", raw(node.allocatable_cpu)], ["RAM allocatable (raw)", raw(node.allocatable_memory)], ["Disk allocatable (raw)", raw(node.allocatable_ephemeral)],
      ...quantities, ["Taints", node.taints?.length ? node.taints.join(", ") : "None"], ["Conditions", node.conditions?.length ? node.conditions.join(", ") : "Not reported"]];
    return ui.disclosure("Technical details", [ui.descriptions(facts),
      ...(omitted ? [ui.text(`${omitted} additional capacity or allocatable fields omitted from this summary. See Exact Kubernetes Node record; its display reports any truncation.`)] : []),
      ui.json(node, "Exact Kubernetes Node record")]);
  }
  function render(data, request, version) {
    const nodes = Array.isArray(data?.nodes) ? data.nodes : [];
    if (!data?.namespace?.name || request.namespace && data.namespace.name !== request.namespace || data.cluster?.context && data.cluster.context !== request.context)
      throw Error("Kubernetes inventory identity differs from the requested context or namespace.");
    const accepted = structuredClone({ context: request.context, namespace: data.namespace, cluster: data.cluster ?? null, nodes });
    ui.batch(() => {
      status.set({ label: `${nodes.length} matching Nodes in ${data.namespace.name}. CPU, memory and storage are Kubernetes allocatable figures, not live ARM admission.` });
      clusterDetails.replace(ui.descriptions([["Context", request.context], ["Namespace", data.namespace.name], ["Namespace UID", raw(data.namespace.uid)]]), ui.json(data.cluster ?? null, "Exact cluster identity"));
      clusterDetails.hidden = false;
      cards.replace(...nodes.map(node => {
        const selectable = node.ready && !node.unschedulable && node.uid && node.os === "linux" && ["amd64", "arm64"].includes(node.arch) && !node.taints?.length;
        const choose = ui.button("Select this Node", () => {
          if (!selectable || !current(request, version)) return;
          selection = { context: request.context, namespace: data.namespace, node };
          selected.replace(ui.heading("Selected Node", 3), ui.descriptions([["Context", request.context], ["Namespace", data.namespace.name], ["Name", raw(node.name)], ...primary(node)]), technical(node),
            ui.text("Use this context, namespace, Node name and UID in a separate current Pod plan. Selection alone creates nothing."));
        }, { disabled: !selectable });
        const card = ui.card(node.name ?? "Unnamed Node", [ui.descriptions([...primary(node), ["Status", selectable ? "Available for ARM Pod review" : "Unavailable for ARM Pod review"]]), technical(node)], { actions: [choose] });
        if (node.uid) cardByUID.set(node.uid, cardByUID.has(node.uid) ? null : card);
        return card;
      }));
    });
    if (!current(request, version)) return false;
    snapshot = accepted; snapshotRequest = request; snapshotVersion = version;
    onKubeInventory(structuredClone(accepted));
    return true;
  }
  async function search() {
    if (disposed) return false;
    const version = ++sequence; clearTimeout(timer); timer = null; clear(); cards.replace();
    if (!opened) return false;
    if (!context.value || !connected()) { status.set({ label: "Select a context while connected." }); return false; }
    status.set({ label: "Reading current Kubernetes Node inventory…" });
    const request = { context: context.value, namespace: namespace.value.trim(), query: query.value.trim(), limit: 50 };
    try {
      const data = await call("kube_nodes", request);
      if (!current(request, version)) return false;
      return render(data, request, version);
    } catch (error) {
      if (!disposed && version === sequence) { cards.replace(); status.set({ label: `Node inventory unavailable: ${error.message}` }); }
      return false;
    }
  }
  function schedule() {
    ++sequence; clear(); cards.replace(); clearTimeout(timer); timer = null;
    if (disposed || !opened) return;
    timer = setTimeout(() => { timer = null; search(); }, 180);
  }
  context.on("change", () => { namespace.value = choices.find(choice => choice.name === context.value)?.namespace ?? ""; search(); });
  namespace.on("input", schedule); namespace.on("change", search); query.on("input", schedule); query.on("change", search);
  function focusKubernetesNode({ uid, context: expectedContext, namespace: expectedNamespace, server, ca_sha256, node } = {}) {
    const name = typeof expectedNamespace === "string" ? expectedNamespace : expectedNamespace?.name;
    if (!snapshot || !current(snapshotRequest, snapshotVersion) || !uid || expectedContext !== snapshot.context || name !== snapshot.namespace.name) return false;
    if (expectedNamespace && typeof expectedNamespace === "object" && Object.hasOwn(expectedNamespace, "uid") && expectedNamespace.uid !== snapshot.namespace.uid) return false;
    if (server !== undefined && server !== snapshot.cluster?.server || ca_sha256 !== undefined && ca_sha256 !== snapshot.cluster?.ca_sha256 || node?.uid !== undefined && node.uid !== uid) return false;
    const card = cardByUID.get(uid); if (!card) return false;
    card.focus(); return true;
  }
  return { getSelection: () => selection, refresh: search, open: openInventory, focusKubernetesNode, updateConnection() {
    if (!connected()) { ++sequence; clear(); cards.replace(); clearTimeout(timer); timer = null;
      status.set({ label: "Disconnected. Refresh the exact Kubernetes context before selecting a Node." }); }
  }, dispose() {
    if (disposed) return;
    disposed = true; ++sequence; clearTimeout(timer); timer = null; clear(); cards.replace();
    root.replace(...root.children.filter(child => child !== inventoryCard));
  } };
}
export {
  createKubeInventory,
  createMeshConsole
};
