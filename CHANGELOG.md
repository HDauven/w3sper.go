# Changelog

## [Unreleased]

### Added

- Add Phoenix note synchronization and proof-backed transfers. [#1]
- Add public and shielded contract deployment. [#1]
- Add owner-aware staking and reward withdrawals. [#1]
- Add committed contract calls. [#1]
- Add contract data-driver loading and JSON–RKYV codecs. [#1]
- Add RUES event subscriptions and transaction execution waits. [#1]

### Changed

- Require explicit `profile.Signer` capabilities for signing and private wallet operations. [#1]
- Upgrade the wallet-core artifact and host ABI together to W3sper.js `97f1cf26`. [#1]
- Reject invalid integer inputs and overflowing fees rather than coercing them. [#1]

### Fixed

- Release SDK-owned network and native resources on cancellation or malformed output. [#1]

[#1]: https://github.com/HDauven/w3sper.go/issues/1
