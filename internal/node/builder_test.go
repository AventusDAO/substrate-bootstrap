package node

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AventusDAO/substrate-bootstrap/internal/config"
)

func rpcConfig() *config.Config {
	return &config.Config{
		Node: config.NodeConfig{
			Binary: "/usr/bin/parachain-node",
			Name:   "rpc-node-1",
		},
		Chain: config.ChainConfig{
			ChainData: config.ChainDataConfig{
				Database: "rocksdb",
				ChainID:  "test_parachain",
			},
			ChainSpec:     "/opt/chainspecs/chain.json",
			Port:          40333,
			BlocksPruning: "archive-canonical",
			StatePruning:  "256",
			Bootnodes:     []string{"/dns/boot1.example.io/tcp/40333/p2p/12D3KooW1"},
			ExtraArgs:     []string{"--db-cache=2048", "--rpc-port=9944", "--rpc-external", "--rpc-cors=all"},
		},
		RelayChain: config.RelayChainConfig{
			ChainData: config.ChainDataConfig{
				Database: "rocksdb",
				ChainID:  "polkadot",
			},
			ChainSpec: "/opt/chainspecs/polkadot.json",
			Port:      30333,
			Bootnodes: []string{"/dns/relay-boot.parity.io/tcp/30333/p2p/12D3KooWR"},
		},
		Prometheus: config.PrometheusConfig{
			Enabled:  true,
			Port:     9615,
			External: true,
		},
		Telemetry: config.TelemetryConfig{
			URLs: []string{"wss://telemetry.example.io/submit 0"},
		},
	}
}

func listenerConfig() *config.Config {
	cfg := rpcConfig()
	cfg.Node.Name = "listener-node-1"
	cfg.Node.EnableKeystore = true
	cfg.Chain.ExtraArgs = []string{"--offchain-worker=always"}
	return cfg
}

func asSolochain(cfg *config.Config) *config.Config {
	cfg.Node.Mode = "solochain"
	return cfg
}

// --- Parachain mode tests ---

func TestBuildArgs_RPCRole(t *testing.T) {
	cfg := rpcConfig()
	args := BuildArgs(cfg, "")

	expected := []string{
		"--name", "rpc-node-1",
		"--base-path", "/data/chain-data",
		"--chain=/opt/chainspecs/chain.json",
		"--database=rocksdb",
		"--no-mdns",
		"--blocks-pruning=archive-canonical",
		"--state-pruning=256",
		"--telemetry-url", "wss://telemetry.example.io/submit 0",
		"--listen-addr=/ip4/0.0.0.0/tcp/40333",
		"--prometheus-port", "9615",
		"--prometheus-external",
		"--db-cache=2048",
		"--rpc-port=9944",
		"--rpc-external",
		"--rpc-cors=all",
		"--bootnodes", "/dns/boot1.example.io/tcp/40333/p2p/12D3KooW1",
		"--",
		"--name", "rpc-node-1",
		"--base-path", "/data/relaychain-data",
		"--database=rocksdb",
		"--telemetry-url", "wss://telemetry.example.io/submit 0",
		"--chain=/opt/chainspecs/polkadot.json",
		"--port", "30333",
		"--bootnodes", "/dns/relay-boot.parity.io/tcp/30333/p2p/12D3KooWR",
	}

	assert.Equal(t, expected, args)
}

func TestBuildArgs_ListenerRole(t *testing.T) {
	cfg := listenerConfig()
	args := BuildArgs(cfg, "")

	expected := []string{
		"--name", "listener-node-1",
		"--base-path", "/data/chain-data",
		"--chain=/opt/chainspecs/chain.json",
		"--database=rocksdb",
		"--no-mdns",
		"--blocks-pruning=archive-canonical",
		"--state-pruning=256",
		"--telemetry-url", "wss://telemetry.example.io/submit 0",
		"--listen-addr=/ip4/0.0.0.0/tcp/40333",
		"--prometheus-port", "9615",
		"--prometheus-external",
		"--keystore-path", "/data/keystore",
		"--offchain-worker=always",
		"--bootnodes", "/dns/boot1.example.io/tcp/40333/p2p/12D3KooW1",
		"--",
		"--name", "listener-node-1",
		"--base-path", "/data/relaychain-data",
		"--database=rocksdb",
		"--telemetry-url", "wss://telemetry.example.io/submit 0",
		"--chain=/opt/chainspecs/polkadot.json",
		"--port", "30333",
		"--bootnodes", "/dns/relay-boot.parity.io/tcp/30333/p2p/12D3KooWR",
	}

	assert.Equal(t, expected, args)
}

func TestBuildArgs_NoTelemetry(t *testing.T) {
	cfg := rpcConfig()
	cfg.Telemetry.URLs = nil
	args := BuildArgs(cfg, "")

	assert.Contains(t, args, "--no-telemetry")
	assert.NotContains(t, args, "--telemetry-url")

	separatorIdx := indexOf(args, "--")
	relayArgs := args[separatorIdx+1:]
	assert.Contains(t, relayArgs, "--no-telemetry")
}

func TestBuildArgs_MultipleTelemetryURLs(t *testing.T) {
	cfg := rpcConfig()
	cfg.Telemetry.URLs = []string{
		"wss://telemetry1.io/submit 0",
		"wss://telemetry2.io/submit 1",
	}
	args := BuildArgs(cfg, "")

	count := 0
	for _, a := range args {
		if a == "--telemetry-url" {
			count++
		}
	}
	assert.Equal(t, 4, count)
}

func TestBuildArgs_OverrideBootnodes(t *testing.T) {
	cfg := rpcConfig()
	cfg.Chain.OverrideBootnodes = []string{"/dns/override/tcp/40333/p2p/OVERRIDE"}

	args := BuildArgs(cfg, "")
	separatorIdx := indexOf(args, "--")
	chainArgs := args[:separatorIdx]

	assert.Contains(t, chainArgs, "/dns/override/tcp/40333/p2p/OVERRIDE")
	assert.NotContains(t, chainArgs, "/dns/boot1.example.io/tcp/40333/p2p/12D3KooW1")
}

func TestBuildArgs_NoBootnodes_UsesChainspec(t *testing.T) {
	cfg := rpcConfig()
	cfg.Chain.Bootnodes = nil
	cfg.Chain.OverrideBootnodes = nil
	cfg.RelayChain.Bootnodes = nil

	args := BuildArgs(cfg, "")

	assert.NotContains(t, args, "--bootnodes")
}

func TestBuildArgs_PrometheusDisabled(t *testing.T) {
	cfg := rpcConfig()
	cfg.Prometheus.Enabled = false
	args := BuildArgs(cfg, "")

	assert.Contains(t, args, "--no-prometheus")
	assert.NotContains(t, args, "--prometheus-port")
	assert.NotContains(t, args, "--prometheus-external")
}

func TestBuildArgs_NoPruning(t *testing.T) {
	cfg := rpcConfig()
	cfg.Chain.BlocksPruning = ""
	cfg.Chain.StatePruning = ""
	args := BuildArgs(cfg, "")

	assert.NotContains(t, args, "--blocks-pruning")
	assert.NotContains(t, args, "--state-pruning")
}

func TestBuildArgs_ExtraArgs(t *testing.T) {
	cfg := rpcConfig()
	cfg.Chain.ExtraArgs = []string{"--wasm-execution=compiled", "--max-runtime-instances=8"}
	args := BuildArgs(cfg, "")

	assert.Contains(t, args, "--wasm-execution=compiled")
	assert.Contains(t, args, "--max-runtime-instances=8")
}

// --- Solochain mode: same config, just mode=solochain ---

func TestBuildArgs_SolochainRPC(t *testing.T) {
	cfg := asSolochain(rpcConfig())
	args := BuildArgs(cfg, "")

	assert.NotContains(t, args, "--")
	assert.Contains(t, args, "--name")
	assert.Contains(t, args, "--rpc-port=9944")
	assert.Contains(t, args, "--db-cache=2048")
	assert.Contains(t, args, "--blocks-pruning=archive-canonical")
	assert.Contains(t, args, "--state-pruning=256")
}

func TestBuildArgs_SolochainListener(t *testing.T) {
	cfg := asSolochain(listenerConfig())
	args := BuildArgs(cfg, "")

	assert.Contains(t, args, "--keystore-path")
	assert.Contains(t, args, "--offchain-worker=always")
	assert.NotContains(t, args, "--db-cache")
	assert.NotContains(t, args, "--rpc-port")
	assert.NotContains(t, args, "--rpc-external")
	assert.NotContains(t, args, "--")
}

func TestBuildArgs_SolochainNoPrometheus(t *testing.T) {
	cfg := asSolochain(rpcConfig())
	cfg.Prometheus.Enabled = false
	args := BuildArgs(cfg, "")

	assert.Contains(t, args, "--no-prometheus")
	assert.NotContains(t, args, "--prometheus-port")
}

func TestBuildArgs_SolochainNoTelemetry(t *testing.T) {
	cfg := asSolochain(rpcConfig())
	cfg.Telemetry.URLs = nil
	args := BuildArgs(cfg, "")

	assert.Contains(t, args, "--no-telemetry")
	assert.NotContains(t, args, "--telemetry-url")
}

func TestBuildArgs_SolochainExtraArgs(t *testing.T) {
	cfg := asSolochain(rpcConfig())
	cfg.Chain.ExtraArgs = []string{"--registered-node-id=5Grwva"}
	args := BuildArgs(cfg, "")

	assert.Contains(t, args, "--registered-node-id=5Grwva")
}

// --- Public IP / --public-addr tests ---

func TestBuildArgs_PublicAddr_ChainNonDefaultPort(t *testing.T) {
	cfg := rpcConfig()
	cfg.Chain.Port = 41333
	args := BuildArgs(cfg, "1.2.3.4")

	separatorIdx := indexOf(args, "--")
	chainArgs := args[:separatorIdx]
	assert.Contains(t, chainArgs, "--public-addr=/ip4/1.2.3.4/tcp/41333")
}

func TestBuildArgs_PublicAddr_ChainDefaultPort(t *testing.T) {
	cfg := rpcConfig()
	cfg.Chain.Port = 40333
	args := BuildArgs(cfg, "1.2.3.4")

	separatorIdx := indexOf(args, "--")
	chainArgs := args[:separatorIdx]
	assert.Contains(t, chainArgs, "--public-addr=/ip4/1.2.3.4/tcp/40333")
}

func TestBuildArgs_NoPublicAddr_WhenEmpty(t *testing.T) {
	cfg := rpcConfig()
	cfg.Chain.Port = 41333
	cfg.RelayChain.Port = 31333
	args := BuildArgs(cfg, "")

	assert.NotContains(t, args, "--public-addr")
}

// The key invariant: chain args are identical regardless of mode.
func TestBuildArgs_SameChainArgsForBothModes(t *testing.T) {
	parachain := rpcConfig()
	solochain := asSolochain(rpcConfig())

	parachainArgs := BuildArgs(parachain, "")
	solochainArgs := BuildArgs(solochain, "")

	separatorIdx := indexOf(parachainArgs, "--")
	chainArgsPara := parachainArgs[:separatorIdx]

	assert.Equal(t, chainArgsPara, solochainArgs)
}

func indexOf(slice []string, val string) int {
	for i, v := range slice {
		if v == val {
			return i
		}
	}
	return -1
}
