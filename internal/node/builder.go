package node

import (
	"fmt"

	"github.com/nicce/substrate-bootstrap/internal/config"
)

// BuildArgs constructs the full CLI arguments for the node binary.
// Chain args are identical for solochain and parachain modes.
// In parachain mode, relay chain args are appended after the "--" separator.
// publicIP, when non-empty, is used for --public-addr (always added when available).
func BuildArgs(cfg *config.Config, publicIP string) []string {
	args := buildChainArgs(cfg, publicIP)

	if !cfg.IsSolochain() {
		args = append(args, "--")
		args = append(args, buildRelayChainArgs(cfg)...)
	}

	return args
}

func buildChainArgs(cfg *config.Config, publicIP string) []string {
	var args []string

	args = append(args,
		"--name", cfg.Node.Name,
		"--base-path", config.ChainDataPath(),
		fmt.Sprintf("--chain=%s", cfg.Chain.ChainSpec),
		fmt.Sprintf("--database=%s", cfg.Chain.ChainData.Database),
		"--no-mdns",
	)

	args = append(args, pruningArgs(cfg.Chain)...)
	args = append(args, telemetryArgs(cfg.Telemetry.URLs)...)
	args = append(args, fmt.Sprintf("--listen-addr=/ip4/0.0.0.0/tcp/%d", cfg.Chain.Port))

	if publicIP != "" {
		args = append(args, fmt.Sprintf("--public-addr=/ip4/%s/tcp/%d", publicIP, cfg.Chain.Port))
	}

	args = append(args, prometheusArgs(cfg.Prometheus)...)

	if cfg.Node.EnableKeystore {
		args = append(args, "--keystore-path", config.KeystorePath())
	}

	args = append(args, cfg.Chain.ExtraArgs...)
	args = append(args, bootnodeArgs(cfg.Chain.Bootnodes, cfg.Chain.OverrideBootnodes)...)

	return args
}

func buildRelayChainArgs(cfg *config.Config) []string {
	var args []string

	args = append(args,
		"--name", cfg.Node.Name,
		"--base-path", config.RelayChainDataPath(),
		fmt.Sprintf("--database=%s", cfg.RelayChain.ChainData.Database),
	)
	args = append(args, telemetryArgs(cfg.Telemetry.URLs)...)

	args = append(args,
		fmt.Sprintf("--chain=%s", cfg.RelayChain.ChainSpec),
		"--port", fmt.Sprintf("%d", cfg.RelayChain.Port),
	)

	for _, bn := range cfg.RelayChain.Bootnodes {
		args = append(args, "--bootnodes", bn)
	}

	return args
}

func pruningArgs(chain config.ChainConfig) []string {
	var args []string
	if chain.BlocksPruning != "" {
		args = append(args, fmt.Sprintf("--blocks-pruning=%s", chain.BlocksPruning))
	}
	if chain.StatePruning != "" {
		args = append(args, fmt.Sprintf("--state-pruning=%s", chain.StatePruning))
	}
	return args
}

func prometheusArgs(prom config.PrometheusConfig) []string {
	if !prom.Enabled {
		return []string{"--no-prometheus"}
	}
	args := []string{"--prometheus-port", fmt.Sprintf("%d", prom.Port)}
	if prom.External {
		args = append(args, "--prometheus-external")
	}
	return args
}

func telemetryArgs(urls []string) []string {
	if len(urls) == 0 {
		return []string{"--no-telemetry"}
	}
	var args []string
	for _, u := range urls {
		args = append(args, "--telemetry-url", u)
	}
	return args
}

func bootnodeArgs(bootnodes, overrides []string) []string {
	nodes := bootnodes
	if len(overrides) > 0 {
		nodes = overrides
	}
	var args []string
	for _, bn := range nodes {
		args = append(args, "--bootnodes", bn)
	}
	return args
}
