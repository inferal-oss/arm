// The graph contains stock controls only. A forwarding token is a private
// in-memory dialog leaf and is never placed in a URL or browser persistence.
import { prepareForwardSource } from "../mesh-forward-console.mjs";
import {createEphemeralInputs} from "./ephemeral-inputs.mjs";
const validKey = (value) => /^[a-f0-9]{32}$/.test(value);
function createForwardConsole({ ui, root, call, connected, cryptoAPI = globalThis.crypto }) {
  let prepared = null, disposed = false, reading = false, privateToken = null, tokenGeneration = 0;
  const message = ui.text("", { role: "status" }), review = ui.stack(), records = ui.stack();
  const fields = {};
  const field = (name, label, value, type = "text") => fields[name] = ui.field(label, { value, type });
  const form = ui.stack([
    ui.heading("Start a local source listener", 3),
    field("workspace", "Workspace ID", ""),
    field("target", "Destination host ID", ""),
    field("service", "Destination service name", ""),
    field("revision", "Destination grant revision", "1", "number"),
    field("bind", "Local loopback bind", "127.0.0.1:0"),
    field("minutes", "Listener lifetime in minutes", "10", "number"),
    field("reason", "Resource reservation reason", "")
  ]);
  const prepare = ui.button("Prepare private token", async () => {
    if (disposed || !connected()) {
      warn("Reconnect before starting a listener.");
      return;
    }
    if (prepared?.sent || prepared?.uncertain) {
      warn(`Inspect listener ${prepared.request.key} before preparing another source.`);
      return;
    }
    if (prepare.disabled) return;
    prepare.disabled = true;
    try {
      prepared = await prepareForwardSource(Object.fromEntries(Object.entries(fields).map(([name, input]) => [name, input.value])), cryptoAPI);
      if (disposed) return;
      showPrepared();
      warn("Save the token privately before starting. It is shown only in this dialog.");
    } catch (error) {
      warn(error.message);
    } finally {
      prepare.disabled = !connected() || Boolean(prepared?.sent || prepared?.uncertain);
    }
  }, { emphasis: "Strong" });
  form.append(prepare);
  root.replace(ui.text("The destination grants a named service. Relays carry bounded transport only. A local token protects each source listener."), message, form, review, records);
  const warn = (label) => {
    if (!disposed) message.set({ label });
  };
  function showPrepared() {
    privateToken?.clear();privateToken=null;
    review.replace();
    if (!prepared) return;
    const { token, request } = prepared;
    privateToken=createEphemeralInputs([{name:"token",label:"Private listener token",initial:token,readOnly:true}]);
    const tokenDisplay=ui.island("forward-private-token-"+(++tokenGeneration),privateToken.widget,{label:"Private listener token"});
    const saved = ui.check("I saved the key and token privately");
    const start = ui.button("Start exact listener", async () => {
      if (start.disabled || disposed) return;
      if (!saved.checked) {
        warn("Save the token before starting.");
        return;
      }
      if (!connected()) {
        warn("Reconnect and inspect this key before any new start.");
        return;
      }
      start.disabled = true;
      prepared.sent = true;
      prepare.disabled = true;
      try {
        const status = await call("mesh_forward_start", request);
        if (disposed) return;
        warn(`Listener ${request.key} is ${status.state} at ${status.bind}. The destination grant is checked on each connection.`);
        await refresh();
      } catch (error) {
        if (!disposed) {
          prepared.uncertain = true;
          prepare.disabled = true;
          warn(`Start outcome for ${request.key} is uncertain: ${error.message}. Keep this token; inspect the exact key before starting again.`);
        }
      }
    }, { emphasis: "Strong" });
    const card = ui.card("Exact source review", [
      ui.descriptions([
        ["Listener key", request.key],
        ["Workspace ID", request.workspace],
        ["Destination host ID", request.target],
        ["Destination service name", request.service],
        ["Destination grant revision", request.grant_revision],
        ["Local loopback bind", request.bind],
        ["Listener lifetime", `${request.ttl_ns} ns (${request.ttl_ns / 60000000000} minutes)`],
        ["Resource reservation reason", request.reason]
      ]),
      ui.text("Form edits do not update this prepared review."),
      ui.text("This token is a local bearer credential. Save the key and token privately. The token cannot be recovered from ARM status."),
      tokenDisplay,
      saved,
      start
    ]);
    ui.put?.(card.iri, "http://www.w3.org/1999/02/22-rdf-syntax-ns#type", "https://arm.local/ui#ForwardPreparedReview", true);
    review.replace(card);
  }
  const statusLine = (row) => `${row.state} \xB7 ${row.key} \xB7 ${row.bind} \u2192 ${row.target}/${row.service} \xB7 grant ${row.grant_revision}${row.failure ? " \xB7 " + row.failure : ""}`;
  function render(rows) {
    const lookup = ui.field("Exact TCP listener key", { placeholder: "Exact 32-hex listener key" });
    const inspect = ui.button("Inspect exact key", async () => {
      if (!connected()) {
        warn("Reconnect before inspecting this exact listener key.");
        return;
      }
      if (!validKey(lookup.value)) {
        warn("Enter an exact 32-hex listener key.");
        return;
      }
      try {
        const row = await call("mesh_forward_status", { key: lookup.value });
        warn(statusLine(row));
      } catch (error) {
        warn(error.message);
      }
    });
    records.replace(ui.heading("Source listeners", 3), ui.stack([lookup, inspect], { row: true }));
    if (!rows.length) {
      records.append(ui.text("No recorded local source listeners. Destination grant review remains in the native CLI."));
      return;
    }
    for (const row of rows) {
      const item = ui.card("Source listener", [ui.text(statusLine(row))]);
      if (row.expires_at) item.append(ui.text(`Expires ${new Date(row.expires_at).toLocaleString()}`));
      if (row.state === "active" || row.state === "ending") {
        const stop = ui.button("Review Stop", () => {
          stop.disabled = true;
          const confirm = ui.button("Stop this listener", async () => {
            if (confirm.disabled || disposed) return;
            if (!connected()) {
              warn("Reconnect before Stop.");
              return;
            }
            confirm.disabled = true;
            try {
              const result = await call("mesh_forward_stop", { key: row.key });
              warn(statusLine(result));
              await refresh();
            } catch (error) {
              warn(`Stop outcome for ${row.key} is uncertain: ${error.message}. Inspect its exact key.`);
            }
          }, { tone: "Danger" });
          item.append(ui.text(`Stop ${row.key} at ${row.bind}; active streams close, and the port/resource charge remains until join and exact probe.`, { tone: "Warning" }), confirm);
        });
        item.append(stop);
      }
      if (row.state === "needs_attention") {
        const recovery = ui.button("Review local recovery", () => {
          recovery.disabled = true;
          const reason = ui.field("TCP listener recovery reason", { placeholder: "Exact cleanup reason" });
          const confirm = ui.button("Recover exact charge", async () => {
            if (confirm.disabled || disposed) return;
            if (!connected() || !reason.value.trim()) {
              warn("Reconnect and enter a meaningful recovery reason.");
              return;
            }
            confirm.disabled = true;
            try {
              const result = await call("mesh_forward_recover", { key: row.key, reason: reason.value });
              warn(statusLine(result));
              await refresh();
            } catch (error) {
              warn(`Recovery for ${row.key} remains uncertain: ${error.message}. Inspect the exact key.`);
            }
          }, { tone: "Danger" });
          item.append(ui.text("Only the old local port/resource charge can be reconciled after the original backend process has ended. Application delivery remains unknown.", { tone: "Warning" }), reason, confirm);
        });
        item.append(recovery);
      }
      records.append(item);
    }
  }
  async function refresh() {
    if (disposed || !connected() || reading) return;
    reading = true;
    try {
      const rows = await call("mesh_forward_list", {});
      if (!disposed) render(Array.isArray(rows) ? rows : []);
    } catch (error) {
      warn(`Source listener list unavailable: ${error.message}`);
    } finally {
      reading = false;
    }
  }
  return { refresh, updateConnection() {
    prepare.disabled = !connected() || Boolean(prepared?.sent || prepared?.uncertain);
    if (!connected()) warn("Disconnected. Inspect exact listener keys after reconnect; no mutation is replayed.");
  }, dispose() {
    disposed = true;
    prepared = null;privateToken?.clear();privateToken=null;
    root.replace();
  } };
}
export {
  createForwardConsole
};
