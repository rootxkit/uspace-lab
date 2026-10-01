# schemas/ansp: read-only mirror

This directory is a byte-for-byte copy of the `schemas/` directory of
[rootxkit/uspace-ansp](https://github.com/rootxkit/uspace-ansp) at the commit
in `SOURCE`. uspace-ansp owns the file (decision record M14, spec `04 §1`,
`00 §7`); this copy is a mirror, not a fork.

- Never edit a file here. Change it in uspace-ansp, then bump the pin with
  `scripts/pin.sh ansp <commit>` in its own
  `build(contracts): pin ansp at <short commit>   [WP-L1 KT-2]` pull request,
  never inside a feature pull request (decision record §4.3).
- `scripts/check-mirrors.sh` re-fetches the file at the pinned commit and
  fails on any byte difference.
- Until `SOURCE` names a commit, nothing is mirrored here and the owning
  repository's own copy + `SOURCE` + CI diff mechanism (decision record
  M11) is the contract.
