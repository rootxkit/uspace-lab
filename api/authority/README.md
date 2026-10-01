# api/authority: read-only mirror

This directory is a byte-for-byte copy of `api/openapi.yaml` of
[rootxkit/uspace-authority](https://github.com/rootxkit/uspace-authority) at the commit
in `SOURCE`. uspace-authority owns the file (decision record M14, spec `04 §1`,
`00 §7`); this copy is a mirror, not a fork.

- Never edit a file here. Change it in uspace-authority, then bump the pin with
  `scripts/pin.sh authority <commit>` in its own
  `build(contracts): pin authority at <short commit>   [WP-L1 KT-2]` pull request,
  never inside a feature pull request (decision record §4.3).
- `scripts/check-mirrors.sh` re-fetches the file at the pinned commit and
  fails on any byte difference.
- Until `SOURCE` names a commit, nothing is mirrored here and the owning
  repository's own copy + `SOURCE` + CI diff mechanism (decision record
  M11) is the contract.
