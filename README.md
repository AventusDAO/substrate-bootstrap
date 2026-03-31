# substrate-bootstrap

A Go CLI that bootstraps and manages Polkadot parachain and solochain nodes. A single static binary handles YAML configuration, bootstrap commands, keystore lifecycle, snapshot sync, and process management.

## Features

- **Parachain & solochain support** — Run full nodes for relay chains, parachains, or standalone chains
- **Configurable node types** — RPC via `chain.extra_args`; keystore via `node.enable_keystore` (listener/hybrid)
- **Snapshot sync** — Download chain data via rclone (Polkadot-style) or tar archives
- **Chainspec download** — Optional `chainspec_url` to fetch chain/relay chain specs; when set, config `chain_spec` is ignored and the node uses the downloaded file
- **Idempotent bootstrap** — Shell commands run once; state tracked in JSON file
- **Fixed data layout** — Predefined volume paths (no user overrides) matching Parity Helm chart patterns; under `chains/`, the directory name follows Substrate (hyphens in YAML `chain_id` become underscores on disk)
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

Configuration is YAML-based with `${ENV_VAR}` expansion. See **[docs/CONFIG.md](docs/CONFIG.md)** for the full reference.

Quick links:

- **Config reference** — [docs/CONFIG.md](docs/CONFIG.md) (sections, defaults, node types, snapshots, public address)
- **Sample config** — [configs/config.yaml](configs/config.yaml)
- **Examples** — [examples/](examples/) (parachain-rpc, parachain-listener, parachain-hybrid, solochain-rpc, solochain-listener)

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
## Project structure

```
.
├── cmd/bootstrap/       # Main entrypoint
├── docs/                # Documentation (CONFIG.md)
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

Licensed under the [GNU General Public License v3.0](LICENSE) (GPL-3.0).
