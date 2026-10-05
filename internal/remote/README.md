# Compatible T3 environments

The protocol follows the pinned remote boundary in `docs/parity/remote-contract.md`.
Discovery validates environment identity and orchestration protocol 2 before
exchanging a one-time pairing credential. Access tokens enter only authenticated
HTTP headers and the native credential store; the websocket URL contains a ticket.
Remote origins require HTTPS; loopback HTTP remains usable for local servers.
Redirects and raw server error bodies are not forwarded to diagnostics.

The framed Effect client correlates object/batch requests, subscription chunks and
ACKs, cancellation interrupts, tagged failures and Ping/Pong. It owns and closes
its connection/read/keepalive workers. Reads can be reconnected by the service;
this client never replays requests. A disconnected or cancelled sent write has an
unknown outcome. Subscription capability projections are replaced by the service
on each connection, preventing old optional arrays from surviving a new host.

Statistics contracts 4–6 are supported. Unknown/invalid array entries are dropped
individually, while an invalid envelope fails the observation. Token data survives
unpriced cost. Per-environment settings patches preserve omitted map entries and
explicit null deletion. Destinations settle separately; retry selects failed
destinations explicitly. The server supplies atomicity, not an invented client CAS.

Credentials use go-keyring v0.2.8: Windows Credential Manager, macOS Keychain and
Linux Secret Service. Native Windows roundtrip uses a uniquely named fixture and
deletes it afterward. Other OS native verification remains deferred by the user's
Windows-first instruction. macOS native store subprocess cancellation is integrated
through the owned credential helper at the managed-process milestone. No plaintext
fallback exists. This module's fixture server cannot spend real reset credits.
