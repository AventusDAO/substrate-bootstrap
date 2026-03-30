package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Minimal YAML -- relies on defaults for ports, pruning, prometheus, logging, etc.
const minimalRPCYAML = `
node:
  name: test-node

chain:
  chain_spec: /opt/chainspecs/parachain.json
  chain_data:
    chain_id: test_parachain
  bootnodes:
    - /dns/boot.example.io/tcp/40333/p2p/12D3KooW

relay_chain:
  chain_spec: /opt/chainspecs/polkadot.json
  bootnodes:
    - /dns/polkadot-boot.parity.io/tcp/30333/p2p/12D3KooW
`

// Full YAML -- overrides all defaults
const fullRPCYAML = `
node:
  binary: /usr/local/bin/custom-node
  name: test-node

chain:
  chain_spec: /opt/chainspecs/parachain.json
  chain_data:
    chain_id: test_parachain
  port: 41333
  blocks_pruning: "1000"
  state_pruning: "1000"
  bootnodes:
    - /dns/boot.example.io/tcp/40333/p2p/12D3KooW

relay_chain:
  chain_spec: /opt/chainspecs/polkadot.json
  port: 31333
  bootnodes:
    - /dns/polkadot-boot.parity.io/tcp/30333/p2p/12D3KooW

prometheus:
  enabled: false
  port: 9999

telemetry:
  urls:
    - "wss://telemetry.example.io/submit 0"

logging:
  level: debug
  format: console
`

const minimalListenerYAML = `
node:
  name: listener-node
  enable_keystore: true

chain:
  chain_spec: /opt/chainspecs/parachain.json
  chain_data:
    chain_id: test_parachain
  bootnodes:
    - /dns/boot.example.io/tcp/40333/p2p/12D3KooW
  extra_args:
    - "--offchain-worker=always"

relay_chain:
  chain_spec: /opt/chainspecs/polkadot.json
  bootnodes:
    - /dns/polkadot-boot.parity.io/tcp/30333/p2p/12D3KooW

bootstrap:
  commands:
    - "echo bootstrap"
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestDefaultConfig(t *testing.T) {
	d := DefaultConfig()
	assert.Equal(t, "/usr/bin/node", d.Node.Binary)
	assert.False(t, d.Node.EnableKeystore)
	assert.Equal(t, 40333, d.Chain.Port)
	assert.Equal(t, "archive-canonical", d.Chain.BlocksPruning)
	assert.Equal(t, "256", d.Chain.StatePruning)
	assert.Equal(t, 30333, d.RelayChain.Port)
	assert.True(t, d.Prometheus.Enabled)
	assert.Equal(t, 9615, d.Prometheus.Port)
	assert.True(t, d.Prometheus.External)
	assert.False(t, d.Keystore.CleanupOnStop)
	assert.Equal(t, "info", d.Logging.Level)
	assert.Equal(t, "json", d.Logging.Format)
	assert.Equal(t, "rocksdb", d.Chain.ChainData.Database)
	assert.Empty(t, d.Chain.ChainData.ChainID)
	assert.Equal(t, "rocksdb", d.RelayChain.ChainData.Database)
	assert.Equal(t, "polkadot", d.RelayChain.ChainData.ChainID)
}

func TestFixedPaths(t *testing.T) {
	t.Setenv("SUBSTRATE_BOOTSTRAP_DATA_DIR", "")
	assert.Equal(t, "/data", DataDir())
	assert.Equal(t, "/data/chain-data", ChainDataPath())
	assert.Equal(t, "/data/relaychain-data", RelayChainDataPath())
	assert.Equal(t, "/data/chain-data/chainspec.json", ChainspecPath())
	assert.Equal(t, "/data/relaychain-data/chainspec.json", RelayChainspecPath())
	assert.Equal(t, "paritydb", DatabaseStorageDir("paritydb"))
	assert.Equal(t, "db", DatabaseStorageDir("rocksdb"))
	assert.Equal(t, "/data/chain-data/chains/avn_staging_dev_testnet/paritydb", ChainDBDataPath("avn_staging_dev_testnet", "paritydb"))
	assert.Equal(t, "/data/chain-data/chains/avn_paseo_v2/db", ChainDBDataPath("avn-paseo-v2", "rocksdb"))
	assert.Equal(t, "/data/chain-data/chains/foo/db", ChainDBDataPath("foo", "rocksdb"))
	assert.Equal(t, "/data/relaychain-data/chains/paseo/paritydb", RelayChainDBDataPath("paseo", "paritydb"))
	assert.Equal(t, "/data/keystore", KeystorePath())
	assert.Equal(t, "/data/bootstrap_state.json", BootstrapStatePath())
}

func TestDataDir_EnvOverride(t *testing.T) {
	t.Setenv("SUBSTRATE_BOOTSTRAP_DATA_DIR", "/tmp/test-data")
	assert.Equal(t, "/tmp/test-data", DataDir())
	assert.Equal(t, "/tmp/test-data/chain-data", ChainDataPath())
	assert.Equal(t, "/tmp/test-data/chain-data/chainspec.json", ChainspecPath())
	assert.Equal(t, "/tmp/test-data/relaychain-data/chainspec.json", RelayChainspecPath())
	assert.Equal(t, "/tmp/test-data/bootstrap_state.json", BootstrapStatePath())
}

func TestLoad_MinimalRPC_UsesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalRPCYAML))
	require.NoError(t, err)

	assert.Equal(t, "/usr/bin/node", cfg.Node.Binary, "should use default binary")
	assert.Equal(t, "test-node", cfg.Node.Name)
	assert.False(t, cfg.Node.EnableKeystore, "should use default enable_keystore")

	assert.Equal(t, "test_parachain", cfg.Chain.ChainData.ChainID)
	assert.Equal(t, "rocksdb", cfg.Chain.ChainData.Database)
	assert.Equal(t, "polkadot", cfg.RelayChain.ChainData.ChainID)

	assert.Equal(t, 40333, cfg.Chain.Port, "should use default chain port")
	assert.Equal(t, "archive-canonical", cfg.Chain.BlocksPruning, "should use default blocks_pruning")
	assert.Equal(t, "256", cfg.Chain.StatePruning, "should use default state_pruning")

	assert.Equal(t, 30333, cfg.RelayChain.Port, "should use default relay port")

	assert.True(t, cfg.Prometheus.Enabled, "should use default prometheus enabled")
	assert.Equal(t, 9615, cfg.Prometheus.Port, "should use default prometheus port")

	assert.Equal(t, "info", cfg.Logging.Level, "should use default log level")
	assert.Equal(t, "json", cfg.Logging.Format, "should use default log format")
}

func TestLoad_FullRPC_OverridesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, fullRPCYAML))
	require.NoError(t, err)

	assert.Equal(t, "/usr/local/bin/custom-node", cfg.Node.Binary, "should override binary")
	assert.Equal(t, 41333, cfg.Chain.Port, "should override chain port")
	assert.Equal(t, "1000", cfg.Chain.BlocksPruning, "should override blocks_pruning")
	assert.Equal(t, "1000", cfg.Chain.StatePruning, "should override state_pruning")
	assert.Equal(t, 31333, cfg.RelayChain.Port, "should override relay port")
	assert.False(t, cfg.Prometheus.Enabled, "should override prometheus enabled")
	assert.Equal(t, "debug", cfg.Logging.Level, "should override log level")
	assert.Equal(t, "console", cfg.Logging.Format, "should override log format")
}

func TestLoad_MinimalListener_UsesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalListenerYAML))
	require.NoError(t, err)

	assert.True(t, cfg.Node.EnableKeystore, "should have enable_keystore")
	assert.False(t, cfg.Keystore.CleanupOnStop, "should use default cleanup_on_stop")
	assert.Len(t, cfg.Bootstrap.Commands, 1)
	assert.Contains(t, cfg.Chain.ExtraArgs, "--offchain-worker=always")
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading config file")
}

func TestLoad_InvalidYAML(t *testing.T) {
	p := writeConfig(t, "{{not yaml}}")
	_, err := Load(p)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config YAML")
}

func TestLoad_MissingName(t *testing.T) {
	yaml := `
node:
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node.name is required")
}

func TestLoad_NoBootnodes_UsesChainspec(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
relay_chain:
  chain_spec: /opt/relay.json
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Empty(t, cfg.Chain.Bootnodes)
	assert.Empty(t, cfg.Chain.OverrideBootnodes)
	assert.Empty(t, cfg.RelayChain.Bootnodes)
}

func TestLoad_OverrideBootnodes(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  override_bootnodes:
    - /dns/override/tcp/40333/p2p/12D3KooW
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Len(t, cfg.Chain.OverrideBootnodes, 1)
	assert.Empty(t, cfg.Chain.Bootnodes)
}

func TestLoad_InvalidPort(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  port: 99999
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "chain.port must be 1-65535")
}

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("TEST_HOST", "my-host-42")
	t.Setenv("TEST_KEY", "secret-key-123")

	input := `name: "${TEST_HOST}" key: "${TEST_KEY}" unset: "${UNSET_VAR_XYZ}"`
	result := expandEnvVars(input)

	assert.Contains(t, result, "my-host-42")
	assert.Contains(t, result, "secret-key-123")
	assert.Contains(t, result, "${UNSET_VAR_XYZ}")
}

func TestLoad_EnvVarExpansion(t *testing.T) {
	t.Setenv("TEST_NODE_NAME", "env-expanded-node")

	yaml := `
node:
  name: "${TEST_NODE_NAME}"
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "env-expanded-node", cfg.Node.Name)
}

func TestLoad_MultipleTelemetryURLs(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
telemetry:
  urls:
    - "wss://telemetry.example.io/submit 0"
    - "wss://telemetry2.example.io/submit 1"
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Len(t, cfg.Telemetry.URLs, 2)
}

func TestValidate_PrometheusInvalidPort(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Node.Name = "n"
	cfg.Chain.ChainSpec = "/spec"
	cfg.Chain.ChainData.ChainID = "test_chain"
	cfg.Chain.Bootnodes = []string{"a"}
	cfg.RelayChain.ChainSpec = "/relay"
	cfg.RelayChain.Bootnodes = []string{"b"}
	cfg.Prometheus.Enabled = true
	cfg.Prometheus.Port = 0

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "prometheus.port")
}

func TestValidate_RequiredEnvVars_Present(t *testing.T) {
	t.Setenv("MY_REQUIRED_VAR", "set")

	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
bootstrap:
  required_env:
    - MY_REQUIRED_VAR
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Len(t, cfg.Bootstrap.RequiredEnv, 1)
}

func TestValidate_RequiredEnvVars_Missing(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
bootstrap:
  required_env:
    - DEFINITELY_NOT_SET_ENV_VAR_XYZ_123
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DEFINITELY_NOT_SET_ENV_VAR_XYZ_123")
	assert.Contains(t, err.Error(), "not set")
}

func TestLoad_MissingChainSpec(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "chain.chain_spec or chain.chainspec_url is required")
}

func TestLoad_MissingRelayChainSpec(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: ""
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "relay_chain.chain_spec or relay_chain.chainspec_url is required")
}

func TestLoad_ChainspecURLMakesChainSpecOptional(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chainspec_url: https://example.com/chainspec.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/chainspec.json", cfg.Chain.ChainspecURL)
	assert.Empty(t, cfg.Chain.ChainSpec)
}

func TestLoad_ChainspecURLMakesRelayChainSpecOptional(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chainspec_url: https://example.com/relay-chainspec.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/relay-chainspec.json", cfg.RelayChain.ChainspecURL)
	assert.Empty(t, cfg.RelayChain.ChainSpec)
}

func TestLoad_NoRelayBootnodes_UsesChainspec(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Len(t, cfg.Chain.Bootnodes, 1)
	assert.Empty(t, cfg.RelayChain.Bootnodes)
}

func TestLoad_InvalidRelayPort(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  port: 0
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "relay_chain.port must be 1-65535")
}

func TestLoad_RelaySnapshotURLWithChainID(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
  chain_data:
    chain_id: paseo
  snapshot_url: https://snapshots.polkadot.io/paseo-muse-paritydb-archive/20260316-011637
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "paseo", cfg.RelayChain.ChainData.ChainID)
}

func TestLoad_ChainSnapshotURLRequiresChainID(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    database: rocksdb
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
  snapshot_url: https://snapshots.polkadot.io/paseo-asset-hub-paritydb-prune/20260316-014747
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "chain.chain_data.chain_id is required")
}

func TestLoad_ChainSnapshotURLWithChainID(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: paseo_asset_hub
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
  snapshot_url: https://snapshots.polkadot.io/paseo-asset-hub-paritydb-prune/20260316-014747
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "paseo_asset_hub", cfg.Chain.ChainData.ChainID)
}

func TestLoad_ChainSnapshotTarURL(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
  snapshot_url: https://example.com/parachain-snapshot.tar.gz
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "test_parachain", cfg.Chain.ChainData.ChainID)
}

func TestLoad_RelaySnapshotTarURL(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
  snapshot_url: https://example.com/snapshot.tar.gz
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "polkadot", cfg.RelayChain.ChainData.ChainID)
}

// --- Solochain mode tests ---

const minimalSolochainYAML = `
node:
  name: solo-node
  mode: solochain

chain:
  chain_spec: /opt/chainspecs/mainnet.json
  chain_data:
    chain_id: solo_mainnet
  blocks_pruning: archive-canonical
  state_pruning: archive-canonical
  bootnodes:
    - /dns/bootnode-01.example.io/tcp/40333/p2p/12D3KooW1
`

func TestLoad_SolochainMinimal(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalSolochainYAML))
	require.NoError(t, err)

	assert.Equal(t, "solochain", cfg.Node.Mode)
	assert.Equal(t, "archive-canonical", cfg.Chain.BlocksPruning)
	assert.Equal(t, "archive-canonical", cfg.Chain.StatePruning)
	assert.True(t, cfg.IsSolochain())
}

func TestLoad_SolochainNoRelayChainRequired(t *testing.T) {
	yaml := `
node:
  name: solo-node
  mode: solochain
chain:
  chain_spec: /opt/chainspecs/mainnet.json
  chain_data:
    chain_id: solo_mainnet
  bootnodes:
    - /dns/boot/tcp/40333/p2p/12D3KooW
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "solochain", cfg.Node.Mode)
}

func TestLoad_ParachainModeRequiresRelayChain(t *testing.T) {
	yaml := `
node:
  name: test
  mode: parachain
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: ""
  bootnodes: []
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "relay_chain.chain_spec or relay_chain.chainspec_url is required")
}

func TestLoad_InvalidMode(t *testing.T) {
	yaml := `
node:
  name: test
  mode: validator
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node.mode must be")
}

func TestIsSolochain(t *testing.T) {
	cfg := DefaultConfig()
	assert.False(t, cfg.IsSolochain())
	cfg.Node.Mode = "solochain"
	assert.True(t, cfg.IsSolochain())
	cfg.Node.Mode = "SOLOCHAIN"
	assert.True(t, cfg.IsSolochain())
}

func TestLoad_RelayChainLightClientSolochainInvalid(t *testing.T) {
	yaml := `
node:
  name: solo-node
  mode: solochain
chain:
  chain_spec: /opt/chainspecs/mainnet.json
  chain_data:
    chain_id: solo_mainnet
  relay_chain_light_client: true
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "chain.relay_chain_light_client is only valid when node.mode is \"parachain\"")
}

func TestLoad_RelayChainLightClientWithRelaySnapshotInvalid(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
  relay_chain_light_client: true
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
  snapshot_url: https://example.com/relay-snap.tar.gz
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "relay_chain.snapshot_url cannot be set when chain.relay_chain_light_client is true")
}

func TestLoad_RelayChainLightClientParachainOK(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
  relay_chain_light_client: true
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.True(t, cfg.Chain.RelayChainLightClient)
}

func TestLoad_InvalidChainDatabase(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
    database: badger
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "chain.chain_data.database must be")
}

func TestLoad_InvalidRelayChainDatabase(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  chain_data:
    chain_id: test_relay
    database: badger
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	_, err := Load(writeConfig(t, yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "relay_chain.chain_data.database must be")
}

func TestLoad_ChainIDTrimmed(t *testing.T) {
	yaml := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: "  test_parachain  "
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	cfg, err := Load(writeConfig(t, yaml))
	require.NoError(t, err)
	assert.Equal(t, "test_parachain", cfg.Chain.ChainData.ChainID)
	assert.Equal(t, "polkadot", cfg.RelayChain.ChainData.ChainID)
}

func TestLoad_InvalidChainIDPathTraversal(t *testing.T) {
	base := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: %s
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	for _, id := range []string{"../x", "../../relaychain-data", "..", "a/b"} {
		t.Run("chain_"+id, func(t *testing.T) {
			_, err := Load(writeConfig(t, fmt.Sprintf(base, id)))
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "chain.chain_data.chain_id must be a single directory name")
		})
	}

	relayBase := `
node:
  name: test
chain:
  chain_spec: /opt/chain.json
  chain_data:
    chain_id: test_parachain
  bootnodes: ["/dns/a/tcp/1/p2p/x"]
relay_chain:
  chain_spec: /opt/relay.json
  chain_data:
    chain_id: %s
  bootnodes: ["/dns/b/tcp/1/p2p/y"]
`
	for _, id := range []string{"../x", "../../chain-data", "..", "a\\b"} {
		t.Run("relay_"+id, func(t *testing.T) {
			_, err := Load(writeConfig(t, fmt.Sprintf(relayBase, id)))
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "relay_chain.chain_data.chain_id must be a single directory name")
		})
	}
}
