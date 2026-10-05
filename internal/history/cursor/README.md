# Cursor account history

The pinned t3code dashboard reader is commit
`51f7f77f87318a5329d3bab15d513fda5654200b`, Git blob
`0931718cea647e3ac39b848091ee03c77a93f0be`. Authentication reads a saved CLI
access token; ephemeral/API authentication disables this source. Account identity
hashes the complete JWT subject, never the token. Local JWT parsing is not signature
validation; the authenticated dashboard validates authorization.

The redirect-free request has a 60 second whole-read deadline and a 10 second
per-page deadline. Only a short terminal page completes pagination. Raw boundary
overlap is removed only when the reported total proves excess rows; identical billed
events otherwise receive stable occurrence indexes. A failed page returns no facts.
Integration retains the prior successful account snapshot and marks it stale.

Reported zero monetary cost remains zero. Fresh input, read/write cache and output
are separate categories. The process-local credential reader shares one native
Keychain prompt, caches a successful token for five minutes and is cancelled on
shutdown. Mac Keychain use requires explicit enablement; its native implementation
is provided by the platform credential adapter. No secrets or raw server bodies are
returned in diagnostic errors or persisted usage data.
