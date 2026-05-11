## Changelog

**Features & Improvements**

- **RPC TLS Listener (mTLS + Fail-Safe)** — Production-grade TLS listener for the JSON-RPC server with optional mutual TLS and fail-safe behavior on cert errors.
- **SIGHUP Cert Reload + Expiry Ticker + getnetworkinfo** — Live cert reload via SIGHUP, periodic expiry monitoring, and TLS state surfaced through getnetworkinfo.
- **reloadrpccerts RPC Handler (Argon2id)** — New RPC for hot cert rotation, gated by an Argon2id-hashed reload passphrase.
- **TCP Rate Limiting + IPv6 /64 Bucketing** — Per-IP RPC connection limiting with correct IPv6 /64 prefix grouping to prevent address-rotation bypass.
- **twins-cli HTTPS Transport + SPKI Pin** — CLI now talks HTTPS to the daemon with SPKI pinning and helper flags for cert/pin management.
- **GUI Settings Schema for RPC TLS** — Settings schema and bridge wiring so the GUI can read/configure RPC TLS options.
- **Hide Masternode Debug Tab When Disabled (Hot-Reload)** — masternode.debug config flag now hides/shows the tab live without restart.
- **Masternode Debug Tab: Payload Caps + Perf Cleanup + Outbound DSEG Coverage** — Bounded event payloads, reduced overhead, and outbound DSEG events are now captured.
- **MultiSend Status in Staking Tooltip** — Staking tooltip now shows MultiSend active state and configuration at a glance.
- **Cap Send Recipients at 100** — Hard cap on the number of recipients per Send transaction to keep tx size predictable.
- **Shared UI Primitives Extracted** — IconButton, PillButton, and Banner consolidated into reusable primitives; Receive page polished against the new tokens.
- **Receive Design Language Propagated to Send & Dialogs** — Send page shell, RecipientField + SendRecipients, Send Coin Control + Fee/Send card, ConfirmationDialog, CustomFeeDialog, and CoinControlDialog all restyled and token-audited to match Receive.
- **Recipient Field: Single-Row Inline Layout** — Recipient input collapsed to a single inline row for higher density.
- **Coin Control Features Card: Restructured & Single-Row Body** — Denser, more discoverable Coin Control card layout with single-row body.
- **Masternodes Debug Dashboard Restyle** — Top section, stats bar, dashboard cards, and overview reworked into a 2×3 card grid.
- **Tighten Send Page Fee+Send Card Layout** — Fee and Send action card spacing tightened for better vertical rhythm.

Bug Fixes

- **CLI Flags After Positional Arguments** — twinsd/twins-cli flags now work regardless of position relative to positional args.
- **MultiSend Active Logic + GUI Binary Cleanup** — Corrected MultiSend active-state evaluation and removed a stale GUI binary from the tree.
- **Masternode Debug Tab Emission Coverage (H-0 Root Cause)** — Fixed missed emissions at H-0 that caused undercounted debug events.
- **Masternode Debug Rate Denominators** — Corrected denominators in rate calculations so per-second/per-minute figures match observed traffic.
- **Masternode Debug Tab Aggregation Populations** — Aggregation buckets now populate correctly across all event types.
- **MN Debug Events Tab Scope & Feedback** — Scoped events tab to the active selection and addressed review feedback on filters/UX.
- **Coin Control Features Label Colors** — Restored intended label colors after the design-language restyle.
