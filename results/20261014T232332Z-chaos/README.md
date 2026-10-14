# 20261014T232332Z-chaos: the disable row again

`authority-rid-disabled` alone (`--rows authority-rid-disabled`),
against the same images as `../20261014T215032Z-chaos/`, after its
audit check was fixed to read both `source_disabled` and
`source_enabled` (lab `f54b2bf`, clean tree, uspace-core v1.6.0).
2026-10-14, 23:23 to 23:30 UTC, same host and procedure.

Verdict: PASS. `direct_rid` was switched off at the authority's console
with the targets file's admin session and seen off in the authority's
own list of switches within 0.5 s. It was held 60 s, switched on and
seen on within 0.5 s. Both switches were found in the audit log as acts
of the same user, carrying the run's reason. The background's own
verdict (missed 0, false 1) is recorded and not judged
(docs/RUNBOOKS/chaos.md says why).
