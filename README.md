# substrate-bootstrap

A Go CLI that bootstraps and manages Polkadot parachain and solochain nodes. A single static binary handles YAML configuration, bootstrap commands, keystore lifecycle, snapshot sync, and process management.

## Features

- **Parachain & solochain support** — Run full nodes for relay chains, parachains, or standalone chains
- **Configurable node types** — RPC via `chain.extra_args`; keystore via `node.enable_keystore` (listener/hybrid)
- **Snapshot sync** — Download chain data via rclone (Polkadot-style) or tar archives
- **Idempotent bootstrap** — Shell commands run once; state tracked in JSON file
- **Fixed data layout** — Predefined volume paths (no user overrides) matching Parity Helm chart patterns
- **Signal forwarding** — Graceful shutdown propagates SIGINT/SIGTERM to the node process

## Requirements

- Go 1.25+
- Node binary (Substrate/Polkadot-compatible)
- `rclone` (optional, for Polkadot-style snapshot downloads)

## Installation

### Build from source

```bash
make build
```

Produces static binaries in `bin/`:

- `bin/substrate-bootstrap-linux-amd64`
- `bin/substrate-bootstrap-linux-arm64`
- `bin/substrate-bootstrap-darwin-arm64`

### Run

```bash
./bin/substrate-bootstrap-linux-amd64 --config /path/to/config.yaml
```

## Configuration

Configuration is YAML-based with `${ENV_VAR}` expansion. See `configs/config.yaml` and `examples/` for samples.

### Key sections

| Section      | Description                                                                 |
| ------------ | --------------------------------------------------------------------------- |
| `node`       | Binary path, name, `enable_keystore` (default false), mode (`parachain`/`solochain`) |
| `chain`      | Chain spec, port, pruning, bootnodes, snapshot URL, `extra_args` (RPC flags, etc.) |
| `relay_chain`| Relay chain config (parachain mode only)                                    |
| `keystore`   | `cleanup_on_stop` (when `enable_keystore: true`)                            |
| `bootstrap`  | Commands to run before node start, required env vars                        |

### Node types

- **RPC-only**: Add `--rpc-port`, `--rpc-external`, `--db-cache`, etc. to `chain.extra_args`. No keystore.
- **Listener-only**: `enable_keystore: true`, no RPC args in `extra_args`. Offchain workers via `--offchain-worker=always`.
- **Hybrid**: `enable_keystore: true` + RPC args in `chain.extra_args`.

### Fixed data volumes

All data paths are hardcoded — **not configurable** in YAML. Mount volumes at these paths:

| Path                     | Purpose                                      |
| ------------------------ | -------------------------------------------- |
| `/data/chain-data`       | Chain data (`--base-path` target)            |
| `/data/relaychain-data`  | Relay chain snapshots (parachain mode only)       |
| `/data/keystore`         | Keystore files (when `enable_keystore: true`)         |
| `/data/bootstrap_state.json` | Bootstrap state tracking                 |

## Usage

### Docker Compose

```yaml
services:
  solochain-rpc:
    image: substrate-bootstrap:latest
    command: ["--config", "/opt/bootstrap/config.yaml"]
    volumes:
      - ./data:/data
      - ./config.yaml:/opt/bootstrap/config.yaml:ro
    ports:
      - "9944:9944"   # JSON-RPC
      - "40333:40333" # P2P
      - "9615:9615"   # Prometheus
```

### Example configs

- `examples/parachain-rpc.yaml` — Parachain RPC node
- `examples/parachain-listener.yaml` — Parachain listener with keystore
- `examples/parachain-hybrid.yaml` — Parachain hybrid (RPC + keystore)
- `examples/solochain-rpc.yaml` — Solochain RPC node
- `examples/solochain-listener.yaml` — Solochain listener with keystore

## Exit codes

| Code | Meaning           |
| ---- | ----------------- |
| 0    | Success           |
| 1    | General error     |
| 2    | Config changed (bootstrap commands hash mismatch; wipe `/data` and restart) |
| 3    | Permission error (e.g. `/data` not writable) |

## Development

```bash
make lint      # golangci-lint
make test      # Unit tests with race detector
make coverage  # Enforce 80% coverage
make integration  # Integration tests
make build     # Cross-compile binaries
```

## Releases

Releases are automated with [Release Please](https://github.com/googleapis/release-please). Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `chore:`, etc.) so Release Please can generate changelogs and version bumps. When a release PR is merged to `main`, a GitHub release is created with pre-built binaries for Linux (amd64, arm64) and macOS (arm64).

## Project structure

```
.
├── cmd/bootstrap/       # Main entrypoint
├── internal/
│   ├── config/         # YAML config, validation, path constants
│   ├── bootstrap/      # Bootstrap state, command execution
│   ├── node/           # CLI arg builder, process runner
│   ├── keystore/       # Keystore directory management
│   ├── snapshot/       # Snapshot download (rclone, tar)
│   └── logging/        # Zap logger setup
├── configs/            # Default config
├── examples/           # Sample configs and docker-compose
├── scripts/            # Build script
└── tests/              # E2E and integration tests
```

## License

See repository for license information.
