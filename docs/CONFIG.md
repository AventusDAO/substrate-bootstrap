# Configuration Reference

Configuration is YAML-based with `${ENV_VAR}` expansion. Only override what differs from defaults; see `internal/config/config.go` for `DefaultConfig()`.

## Overview

| Section      | Purpose |
| ------------ | ------- |
| `node`       | Binary, name, mode, keystore |
| `chain`      | Parachain/solochain chain spec, port, pruning, bootnodes, snapshot, extra args |
| `relay_chain`| Relay chain config (parachain mode only) |
| `prometheus` | Metrics endpoint |
| `telemetry`  | Telemetry URLs |
| `keystore`   | Keystore cleanup behavior |
| `bootstrap`  | Pre-start commands and required env vars |
| `logging`    | Log level and format |

## Section Reference

### node

| Field           | Default      | Description |
| --------------- | ------------ | ----------- |
| `binary`        | `/usr/bin/node` | Path to the node binary |
| `name`          | (required)   | Node name; use `${HOSTNAME}` for dynamic |
| `mode`          | `parachain`  | `parachain` or `solochain` |
| `enable_keystore` | `false`    | When `true`, adds `--keystore-path` and runs keystore lifecycle |

### chain

Parachain args (before `--`) in parachain mode; main chain config in solochain mode.

#### `chain_data` (required)

Mirrors Parity node chart [`chainData`](https://github.com/paritytech/helm-charts/blob/main/charts/node/values.yaml): database backend and the Substrate chain data directory **name** (single path segment under `chains/`, usually matching chainspec `id`). This is **not** a full filesystem path; `--base-path` stays `/data/chain-data`.

| Field       | Default    | Description |
| ----------- | ---------- | ----------- |
| `database`  | `rocksdb`  | `rocksdb` (DB dir `db`) or `paritydb` (DB dir `paritydb`); passed as `--database` |
| `chain_id`  | (required) | Logical id (same role as helm `CHAIN_PATH` / `node.chain`); on disk under `chains/` Substrate uses hyphens→underscores (e.g. `avn-paseo-v2` → `avn_paseo_v2`) |

#### Other `chain` fields

| Field                  | Default              | Description |
| ---------------------- | -------------------- | ----------- |
| `chain_spec`           | (required*)          | Path to chainspec JSON |
| `chainspec_url`        | `""`                 | When set, downloads chainspec; `chain_spec` ignored |
| `force_download_chainspec` | `false`          | Overwrite existing chainspec when downloading |
| `port`                 | `40333`              | P2P listen port; uses `--listen-addr /ip4/0.0.0.0/tcp/{port}` |
| `blocks_pruning`       | `archive-canonical`  | `archive`, `archive-canonical`, or custom |
| `state_pruning`        | `"256"`              | State pruning; use `archive` to keep all |
| `bootnodes`            | `[]`                 | Chain bootnodes |
| `override_bootnodes`   | `[]`                 | When set, replaces `bootnodes` |
| `extra_args`           | `[]`                 | Extra CLI args (RPC, offchain-worker, etc.) |
| `snapshot_url`         | `""`                 | Snapshot URL (rclone or tar); data goes under `chains/<substrate_dir>/` (see `chain_id` row) |
| `relay_chain_light_client` | `false`          | **Parachain only.** Adds `--relay-chain-light-client` to chain args (experimental; full-node embedded relay light client using `relay_chain` chainspec). Disables relay-chain snapshot sync; do not set `relay_chain.snapshot_url`. |

\* Either `chain_spec` or `chainspec_url` is required.

### relay_chain

Parachain mode only; args after `--` separator.

#### `relay_chain.chain_data`

Same semantics as `chain.chain_data` for relay data under `/data/relaychain-data`.

| Field       | Default     | Description |
| ----------- | ----------- | ----------- |
| `database`  | `rocksdb`   | `rocksdb` or `paritydb` |
| `chain_id`  | `polkadot`  | Relay logical id; on-disk segment under `chains/` uses Substrate naming (hyphens→underscores) |

#### Other `relay_chain` fields

| Field                  | Default   | Description |
| ---------------------- | --------- | ----------- |
| `chain_spec`           | (required*)| Path to relay chainspec |
| `chainspec_url`        | `""`      | When set, downloads chainspec |
| `force_download_chainspec` | `false` | Overwrite when downloading |
| `port`                 | `30333`   | P2P port; uses `--port` |
| `bootnodes`            | `[]`      | Relay bootnodes |
| `snapshot_url`         | `""`      | Relay snapshot URL |

### prometheus

| Field     | Default | Description |
| --------- | ------- | ----------- |
| `enabled` | `true`  | Enable Prometheus metrics |
| `port`    | `9615`  | Metrics port |
| `external`| `true`  | Bind to external interface |

### telemetry

| Field | Default | Description |
| ----- | ------- | ----------- |
| `urls` | `[]`    | Telemetry URLs; empty = `--no-telemetry` |

### keystore

| Field            | Default | Description |
| ---------------- | ------- | ----------- |
| `cleanup_on_stop`| `false` | Remove keystore files on graceful shutdown |

### bootstrap

| Field         | Default | Description |
| ------------- | ------- | ----------- |
| `required_env`| `[]`    | Fail if these env vars are not set |
| `commands`    | `[]`    | Shell commands to run before node start |

Commands require `/bin/sh` or BusyBox. Distroless images typically do not include a shell.

### logging

| Field  | Default | Description |
| ------ | ------- | ----------- |
| `level`| `info` | `debug`, `info`, `warn`, `error` |
| `format`| `json` | `json`, `console`, or `node` (substrate-node style) |

## Data Volumes

All paths are fixed and not configurable in YAML. Mount volumes at:

| Path | Purpose |
| ---- | ------- |
| `/data/chain-data` | Chain data (`--base-path`) |
| `/data/chain-data/chainspec.json` | Downloaded chainspec (when `chainspec_url` set) |
| `/data/relaychain-data` | Relay chain data (parachain only) |
| `/data/relaychain-data/chainspec.json` | Downloaded relay chainspec |
| `/data/keystore` | Keystore files (when `enable_keystore: true`) |
| `/data/bootstrap_state.json` | Bootstrap state tracking |

## Node Types

- **RPC-only**: Add `--rpc-port`, `--rpc-external`, `--db-cache`, etc. to `chain.extra_args`. No keystore.
- **Listener-only**: `enable_keystore: true`, no RPC args. Add `--offchain-worker=always` in `extra_args`.
- **Hybrid**: `enable_keystore: true` plus RPC args in `extra_args`.

## Chainspec Download

Set `chainspec_url` under `chain` or `relay_chain` to download the chainspec before the node starts. When set, `chain_spec` is ignored and the node uses the file at the fixed path. Use `force_download_chainspec: true` to overwrite an existing file.

## Snapshot Sync

- **Tar archives**: Use a URL ending in `.tar.gz`, `.tar.lz4`, `.tar.zst`, etc. Extracted to `/data/chain-data/chains/<normalized_chain_id>/<db|paritydb>/` (normalized = Substrate dir name: hyphens in `chain_id` become underscores; relay analogue under relaychain-data).
- **Polkadot-style** (rclone): Use base URL (e.g. `https://snapshots.polkadot.io/...`). `chain_id` must match the snapshot chain id. Base URLs without a version suffix auto-resolve the latest snapshot.
- **Backend vs snapshot**: Official Polkadot snapshots use ParityDB — set `chain_data.database: paritydb` (and the same for relay) so files land under `paritydb/` where the node expects them. Default `rocksdb` uses the `db/` directory.

## Public Address

When `chain.port` is non-default (not 40333), the public IP is auto-detected from ifconfig.io at startup. When successful, `--public-addr=/ip4/{ip}/tcp/{port}` is added for the chain (parachain/solochain), allowing peers to discover the node. The relay chain uses only `--port`. Set `SUBSTRATE_BOOTSTRAP_DISABLE_PUBLIC_IP=1` to skip the lookup (e.g. in environments without outbound internet).

## Environment Variables

Use `${VAR_NAME}` in the config; values are expanded at load time. Example: `name: "${HOSTNAME}"`.

## Example Configs

- `configs/config.yaml` — Sample config with comments
- `examples/parachain-rpc.yaml` — Parachain RPC node
- `examples/parachain-listener.yaml` — Parachain listener with keystore
- `examples/parachain-hybrid.yaml` — Parachain hybrid (RPC + keystore)
- `examples/solochain-rpc.yaml` — Solochain RPC node
- `examples/solochain-listener.yaml` — Solochain listener with keystore
