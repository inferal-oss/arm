# Original child supervision and SSH settlement

Supervised commands and Cargo helpers retain the original started command and
its process-group identity. Native non-reaping observation precedes the sole
`Wait`; signal authority is retired before reaping. A persisted PID cannot
reconstruct that authority. Observation, signaling and join failures retain the
resource promise rather than certify cleanup.

A successful KILL request is idempotent. It is not a completion witness. The
Manager additionally requires the original native join and a read-only check
that the group is absent before releasing its process resource charge.

Darwin does not signal zombie-only groups. Before a signal, supervision observes
the original child without reaping it. If terminal, a fixed two-entry
`proc_listpids` snapshot can confirm that this unreaped leader is the group's
sole member. Only that exact positive witness retires signal authority while
preserving the original sole `Wait`. A bounded 250 ms grace allows a transient
second member to disappear; otherwise the real signal is attempted. The final
waiter uses the same witness before its last KILL. Signal preflight observation and membership
errors disable uncertain controls; prior failures and actual signal denials
remain failures after a later positive join. Linux uses no membership grace or
signal preflight.

Cargo cancellation checks the current worker and immutable lease binding before
cancelling it. Cancellation still reaches that original worker when recording
its ending state fails. The prior durable row stays charged until the original
callback and any native process have positively joined; a refused journal write
cannot make backend shutdown wait on an uncancelled callback.

Cargo cancellation invokes the original signal owner **before closing IPC**.
Closing IPC first can trigger the helper's parent-loss action and let its own
group kill precede the parent's cancellation attempt. Both the cancellation
watcher and final cleanup preserve this ordering. Real signal denials remain
sticky; IPC closure and worker completion do not establish native cleanup.
Unknown observation or control remains charged. Reclamation requires the
original native join, verified group absence and any required transport joins;
a later positive observation does not erase an earlier failure.

SSH native completion and resource settlement are separate events. Native
readiness lets the original SSH server send exit status and close its
connection. Charges remain until that connection, input and output copies, and
cancellation callback have positively joined. Settlement also checks the
original lease intent, native binding and exact staged durable row. Failed
launches carrying real I/O obey the same rule. Explicit reconciliation cannot
bypass an outstanding original transport hold.

On restart, an unsettled saved SSH descriptor classifies an obligation that
needs attention. It does not prove transport closure or authorize signaling.
Existing specialized sync recovery keeps its own retained outcome. A terminal
record already committed after a witnessed join remains terminal.

This is process-group supervision, not a sandbox or proof about descendants
that deliberately escape the original group. Unknown cleanup remains charged.
The implementation does not claim uninterruptible kernel calls make bounded
progress. Suspend-inclusive lease entitlement is a separate clock component.
