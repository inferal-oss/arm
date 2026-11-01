# Checked Claude chat activation: current limitation

Automatic activation of checked Claude chats remains unsupported. The current
manual flow preserves exact chat selection; this investigation does not qualify
a native delivery route.

Claude documents a local inbox socket that scripts can post to, subject to its
native inbound message controls. See the
[official inbox documentation](https://code.claude.com/docs/en/cross-session-messaging#the-sessions-inbox-socket).

Static investigation of Claude Code 2.1.308 found a conversation identity gap.
The receiver checks the supplied conversation ID before asynchronous inbound
processing, then computes the queue recipient from the current conversation
after that processing. The same process can switch conversations while retaining
its inbox. A message that initially matched a checked conversation could
therefore be queued for a different conversation after a switch.

ARM can verify the selected chat, native process generation, socket owner and
socket identity before writing. Those observations cannot fence a transition
inside the receiver. Reporting a write as unconfirmed permits uncertainty about
acceptance; it does not permit targeting an unchecked conversation. Resuming or
forking a chat is not a substitute.

For now, open the exact checked chat, verify its original session identity and
working directory, and follow ARM's displayed activation instructions there.
Check the current project policy and acknowledge only when eligible. Keep an
unknown original start unknown. ARM does not message unchecked chats through
this flow.

Direct activation requires a conversation-bound native transport or an upstream
lifecycle fence that preserves the selected conversation through admission and
queueing. This finding comes from static investigation, not a failed native
delivery test. No real chat was contacted during the investigation.
