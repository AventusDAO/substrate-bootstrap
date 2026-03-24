package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/AventusDAO/substrate-bootstrap/internal/logging"
)

const defaultDataDir = "/data"

// DataDir returns the root data directory. When SUBSTRATE_BOOTSTRAP_DATA_DIR is set,
// that path is used (for integration tests); otherwise /data.
func DataDir() string {
	if d := os.Getenv("SUBSTRATE_BOOTSTRAP_DATA_DIR"); d != "" {
		return filepath.Clean(d)
	}
	return defaultDataDir
}

func ChainDataPath() string      { return filepath.Join(DataDir(), "chain-data") }
func RelayChainDataPath() string { return filepath.Join(DataDir(), "relaychain-data") }

func ChainspecPath() string      { return filepath.Join(ChainDataPath(), "chainspec.json") }
func RelayChainspecPath() string { return filepath.Join(RelayChainDataPath(), "chainspec.json") }

// DatabaseStorageDir returns the per-chain database directory name under chains/<chain_id>/.
// Matches Parity helm node.databasePath: paritydb -> "paritydb", else "db" (rocksdb).
func DatabaseStorageDir(database string) string {
	if strings.EqualFold(strings.TrimSpace(database), "paritydb") {
		return "paritydb"
	}
	return "db"
}

// ChainDBDataPath returns the chain snapshot / DB path:
// base-path/chains/<chainID>/<storageDir>/
func ChainDBDataPath(chainID, database string) string {
	return filepath.Join(ChainDataPath(), "chains", chainID, DatabaseStorageDir(database))
}

// RelayChainDBDataPath returns the relay chain snapshot / DB path.
func RelayChainDBDataPath(chainID, database string) string {
	return filepath.Join(RelayChainDataPath(), "chains", chainID, DatabaseStorageDir(database))
}

func KeystorePath() string       { return filepath.Join(DataDir(), "keystore") }
func BootstrapStatePath() string { return filepath.Join(DataDir(), "bootstrap_state.json") }

type Config struct {
	Node       NodeConfig       `yaml:"node"`
	Chain      ChainConfig      `yaml:"chain"`
	RelayChain RelayChainConfig `yaml:"relay_chain"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
	Telemetry  TelemetryConfig  `yaml:"telemetry"`
	Keystore   KeystoreConfig   `yaml:"keystore"`
	Bootstrap  BootstrapConfig  `yaml:"bootstrap"`
	Logging    logging.Config   `yaml:"logging"`
}

type NodeConfig struct {
	Binary         string `yaml:"binary"`
	Name           string `yaml:"name"`
	Mode           string `yaml:"mode"`
	EnableKeystore bool   `yaml:"enable_keystore"`
}

// ChainDataConfig mirrors Parity node chart chainData (database backend + chain id directory segment).
type ChainDataConfig struct {
	Database string `yaml:"database"` // rocksdb (default) or paritydb
	ChainID  string `yaml:"chain_id"` // Substrate chains/<chain_id>/ segment; not a full path
}

// ChainConfig holds chain-specific settings.
// In parachain mode these are the parachain args (before the -- separator).
// In solochain mode this is the main (and only) chain config.
type ChainConfig struct {
	ChainData              ChainDataConfig `yaml:"chain_data"`
	ChainSpec              string          `yaml:"chain_spec"`
	ChainspecURL           string          `yaml:"chainspec_url"`            // when set, chain_spec is ignored; downloaded to ChainspecPath()
	ForceDownloadChainspec bool            `yaml:"force_download_chainspec"` // overwrite existing file
	Port                   int             `yaml:"port"`
	BlocksPruning          string          `yaml:"blocks_pruning"`
	StatePruning           string          `yaml:"state_pruning"`
	Bootnodes              []string        `yaml:"bootnodes"`
	OverrideBootnodes      []string        `yaml:"override_bootnodes"`
	ExtraArgs              []string        `yaml:"extra_args"`
	SnapshotURL            string          `yaml:"snapshot_url"`
}

type RelayChainConfig struct {
	ChainData              ChainDataConfig `yaml:"chain_data"`
	ChainSpec              string          `yaml:"chain_spec"`
	ChainspecURL           string          `yaml:"chainspec_url"`            // when set, chain_spec is ignored; downloaded to RelayChainspecPath()
	ForceDownloadChainspec bool            `yaml:"force_download_chainspec"` // overwrite existing file
	Port                   int             `yaml:"port"`
	Bootnodes              []string        `yaml:"bootnodes"`
	SnapshotURL            string          `yaml:"snapshot_url"`
}

type PrometheusConfig struct {
	Enabled  bool `yaml:"enabled"`
	Port     int  `yaml:"port"`
	External bool `yaml:"external"`
}

type TelemetryConfig struct {
	URLs []string `yaml:"urls"`
}

type KeystoreConfig struct {
	CleanupOnStop bool `yaml:"cleanup_on_stop"`
}

type BootstrapConfig struct {
	Commands    []string `yaml:"commands"`
	RequiredEnv []string `yaml:"required_env"`
}

// DefaultConfig returns the base configuration with sensible defaults.
// YAML config is merged on top -- only fields present in the YAML override these.
func DefaultConfig() Config {
	return Config{
		Node: NodeConfig{
			Binary:         "/usr/bin/node",
			Mode:           "parachain",
			EnableKeystore: false,
		},
		Chain: ChainConfig{
			ChainData: ChainDataConfig{
				Database: "rocksdb",
			},
			Port:          40333,
			BlocksPruning: "archive-canonical",
			StatePruning:  "256",
		},
		RelayChain: RelayChainConfig{
			ChainData: ChainDataConfig{
				Database: "rocksdb",
				ChainID:  "polkadot",
			},
			Port: 30333,
		},
		Prometheus: PrometheusConfig{
			Enabled:  true,
			Port:     9615,
			External: true,
		},
		Logging: logging.Config{
			Level:  "info",
			Format: "json",
		},
	}
}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func Load(path string) (*Config, error) {
	// #nosec G304 -- path is the explicit --config argument from the operator
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	expanded := expandEnvVars(string(data))

	cfg := DefaultConfig()
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}

	cfg.applyChainDataDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// applyChainDataDefaults fills chain_data fields that YAML omitted (nested struct zero-value wipes defaults).
func (c *Config) applyChainDataDefaults() {
	if strings.TrimSpace(c.Chain.ChainData.Database) == "" {
		c.Chain.ChainData.Database = "rocksdb"
	}
	c.Chain.ChainData.Database = strings.ToLower(strings.TrimSpace(c.Chain.ChainData.Database))

	if strings.TrimSpace(c.RelayChain.ChainData.Database) == "" {
		c.RelayChain.ChainData.Database = "rocksdb"
	}
	c.RelayChain.ChainData.Database = strings.ToLower(strings.TrimSpace(c.RelayChain.ChainData.Database))

	c.Chain.ChainData.ChainID = strings.TrimSpace(c.Chain.ChainData.ChainID)
	c.RelayChain.ChainData.ChainID = strings.TrimSpace(c.RelayChain.ChainData.ChainID)
	if c.RelayChain.ChainData.ChainID == "" {
		c.RelayChain.ChainData.ChainID = "polkadot"
	}
}

// isSafeChainDirSegment reports whether id is safe to use as a single path segment under chains/<chain_id>/.
func isSafeChainDirSegment(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.ContainsAny(id, `/\`) {
		return false
	}
	return filepath.Clean(id) == id
}

func expandEnvVars(input string) string {
	return envVarPattern.ReplaceAllStringFunc(input, func(match string) string {
		varName := match[2 : len(match)-1]
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match
	})
}

func (c *Config) IsSolochain() bool {
	return strings.ToLower(c.Node.Mode) == "solochain"
}

func (c *Config) Validate() error {
	var errs []string

	if c.Node.Binary == "" {
		errs = append(errs, "node.binary is required")
	}
	if c.Node.Name == "" {
		errs = append(errs, "node.name is required")
	}

	mode := strings.ToLower(c.Node.Mode)
	if mode != "parachain" && mode != "solochain" {
		errs = append(errs, fmt.Sprintf("node.mode must be \"parachain\" or \"solochain\", got %q", c.Node.Mode))
	}

	if c.Chain.ChainSpec == "" && c.Chain.ChainspecURL == "" {
		errs = append(errs, "chain.chain_spec or chain.chainspec_url is required")
	}

	if c.Chain.ChainData.ChainID == "" {
		errs = append(errs, "chain.chain_data.chain_id is required (Substrate chains/<chain_id>/ directory name, not a full path)")
	} else if !isSafeChainDirSegment(c.Chain.ChainData.ChainID) {
		errs = append(errs, "chain.chain_data.chain_id must be a single directory name (no path separators or ..)")
	}

	switch c.Chain.ChainData.Database {
	case "rocksdb", "paritydb":
	default:
		errs = append(errs, fmt.Sprintf("chain.chain_data.database must be \"rocksdb\" or \"paritydb\", got %q", c.Chain.ChainData.Database))
	}

	if c.Chain.Port <= 0 || c.Chain.Port > 65535 {
		errs = append(errs, fmt.Sprintf("chain.port must be 1-65535, got %d", c.Chain.Port))
	}

	if mode != "solochain" {
		if c.RelayChain.ChainSpec == "" && c.RelayChain.ChainspecURL == "" {
			errs = append(errs, "relay_chain.chain_spec or relay_chain.chainspec_url is required")
		}
		if c.RelayChain.Port <= 0 || c.RelayChain.Port > 65535 {
			errs = append(errs, fmt.Sprintf("relay_chain.port must be 1-65535, got %d", c.RelayChain.Port))
		}
		if c.RelayChain.ChainData.ChainID == "" {
			errs = append(errs, "relay_chain.chain_data.chain_id is required (Substrate chains/<chain_id>/ directory name, not a full path)")
		} else if !isSafeChainDirSegment(c.RelayChain.ChainData.ChainID) {
			errs = append(errs, "relay_chain.chain_data.chain_id must be a single directory name (no path separators or ..)")
		}
		switch c.RelayChain.ChainData.Database {
		case "rocksdb", "paritydb":
		default:
			errs = append(errs, fmt.Sprintf("relay_chain.chain_data.database must be \"rocksdb\" or \"paritydb\", got %q", c.RelayChain.ChainData.Database))
		}
	}

	if c.Prometheus.Enabled && (c.Prometheus.Port <= 0 || c.Prometheus.Port > 65535) {
		errs = append(errs, fmt.Sprintf("prometheus.port must be 1-65535, got %d", c.Prometheus.Port))
	}

	for _, envVar := range c.Bootstrap.RequiredEnv {
		if os.Getenv(envVar) == "" {
			errs = append(errs, fmt.Sprintf("required environment variable %s is not set", envVar))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
