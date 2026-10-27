# Semblance screen integration

The Screen view uses stock Semblance Stacks, Text, Buttons, Checkbox, Choice and
Dialogs. Its fullscreen state is `fs:fullViewport` and is followed through the
runtime input stream, including the stock Escape exit. View-only mode, explicit
clipboard transfers, separate-user mode, authentication prompts and connection
status remain explicit controls. The unchanged noVNC and ARD sources are bundled
locally on demand; no network library fetch or external runtime is involved.

There are two narrowly scoped uses of the implemented `ext:Island` integration:

* **Framebuffer:** the existing patched noVNC renderer owns its canvas subtree.
  The widget sets only its viewport geometry; the graph owns the surrounding
  controls, dialog state and full-view request. A foreign RFB canvas is required
  for interactive remote framebuffer input and decoding. This is not a host for
  the old application.
* **Ephemeral native text inputs:** only Mac authentication, the optional
  separate-user account, and clipboard text use private native inputs. Stock
  TextField/TextArea input reactions store `c:value`, while Semblance's LDES
  records the input event's `beh:value`. Putting these sensitive values through
  those controls would retain passwords or copied private text in the graph.
  The private widget declares **no output events**. Labels and its lifecycle are
  graph components; values are accessed only by explicit stock action buttons.
  Native input nodes and initial-value closures clear on submit, close, cancel
  and disconnect. Password values only reach the active noVNC authentication
  object. The RFB token stays in the host closure and WebSocket subprotocol.

`createEphemeralInputs` is a security integration boundary, not a new generic
framework field implementation. A reusable framework facility for secret or
transient values needs an explicit privacy/event-retention design before it
could replace this boundary. No new upstream component gap or publication is
claimed by these changes.

Focused tests use an injected RFB stand-in and native input fixture. They verify
explicit connection, exact token transport, lazy-load cancellation, authentication
and clipboard data exclusion from graph-facing component arguments, clearing,
view-only refusal and UTF-8 bounds. They do **not** establish real Mac sign-in,
real framebuffer rendering, actual RDF/LDES serialization, stock modal focus or
narrow-layout acceptance. Those are composed runtime/browser validation gates.
