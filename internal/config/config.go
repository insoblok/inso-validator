package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level validator configuration.
type Config struct {
	Validator     ValidatorConfig     `yaml:"validator"`
	Sequencer     SequencerConfig     `yaml:"sequencer"`
	L1            L1Config            `yaml:"l1"`
	TasteScore    TasteScoreConfig    `yaml:"tastescore"`
	Consensus     ConsensusConfig     `yaml:"consensus"`
	Sovereignty   SovereigntyConfig   `yaml:"sovereignty"`
	AdaptiveBlock AdaptiveBlockConfig `yaml:"adaptive_block"`
	Logging       LoggingConfig       `yaml:"logging"`
	Metrics       MetricsConfig       `yaml:"metrics"`
}

// ValidatorConfig holds the core validator settings.
type ValidatorConfig struct {
	DataDir    string   `yaml:"datadir"`
	ListenAddr string   `yaml:"listen_addr"`
	P2PPort    int      `yaml:"p2p_port"`
	MinStake   uint64   `yaml:"min_stake"`
	KeyFile    string   `yaml:"keyfile"`
	Bootnodes  []string `yaml:"bootnodes"`
}

// SequencerConfig holds the sequencer connection settings.
type SequencerConfig struct {
	RPCURL string `yaml:"rpc_url"`
	WSURL  string `yaml:"ws_url"`
}

// L1Config holds L1 Ethereum connection settings.
type L1Config struct {
	RPCURL          string `yaml:"rpc_url"`
	ChainID         uint64 `yaml:"chain_id"`
	StakingContract string `yaml:"staking_contract"`
}

// TasteScoreConfig holds TasteScore integration settings.
type TasteScoreConfig struct {
	Enabled bool   `yaml:"enabled"`
	APIURL  string `yaml:"api_url"`
	APIKey  string `yaml:"api_key"`
}

// ConsensusConfig holds consensus engine settings.
type ConsensusConfig struct {
	BlockTimeout     time.Duration `yaml:"block_timeout"`
	AttestationDelay time.Duration `yaml:"attestation_delay"`
	SlashingEnabled  bool          `yaml:"slashing_enabled"`
	MinValidators    int           `yaml:"min_validators"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// MetricsConfig holds Prometheus metrics settings.
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
}

// SovereigntyConfig holds sovereignty engine settings for the validator.
type SovereigntyConfig struct {
	Enabled         bool    `yaml:"enabled"`
	XPDecayRate     float64 `yaml:"xp_decay_rate"`     // daily XP decay (0.005 = 0.5%)
	AttestationXP   uint64  `yaml:"attestation_xp"`    // XP per block attestation
	UptimeBonusXP   uint64  `yaml:"uptime_bonus_xp"`   // XP per uptime check
	SlashPenaltyXP  uint64  `yaml:"slash_penalty_xp"`  // XP lost per slash
}

// AdaptiveBlockConfig defines the expected adaptive block bounds for validation.
type AdaptiveBlockConfig struct {
	Enabled     bool   `yaml:"enabled"`
	MinGasLimit uint64 `yaml:"min_gas_limit"`
	MaxGasLimit uint64 `yaml:"max_gas_limit"`
	MinMaxTx    int    `yaml:"min_max_tx"`
	MaxMaxTx    int    `yaml:"max_max_tx"`
}

// Load reads and parses a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(data))

	cfg := DefaultConfig()
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

// DefaultConfig returns sensible development defaults.
func DefaultConfig() *Config {
	return &Config{
		Validator: ValidatorConfig{
			DataDir:    "/data",
			ListenAddr: "0.0.0.0:8547",
			P2PPort:    30303,
			MinStake:   10000,
		},
		Sequencer: SequencerConfig{
			RPCURL: "http://localhost:8545",
			WSURL:  "ws://localhost:8546",
		},
		L1: L1Config{
			RPCURL:  "http://localhost:8551",
			ChainID: 1,
		},
		TasteScore: TasteScoreConfig{
			Enabled: true,
			APIURL:  "https://api.insoblokai.io",
		},
		Consensus: ConsensusConfig{
			BlockTimeout:     2 * time.Second,
			AttestationDelay: 200 * time.Millisecond,
			SlashingEnabled:  true,
			MinValidators:    1,
		},
		Sovereignty: SovereigntyConfig{
			Enabled:        true,
			XPDecayRate:    0.005,
			AttestationXP:  10,
			UptimeBonusXP:  5,
			SlashPenaltyXP: 500,
		},
		AdaptiveBlock: AdaptiveBlockConfig{
			Enabled:     true,
			MinGasLimit: 15_000_000,
			MaxGasLimit: 60_000_000,
			MinMaxTx:    1_000,
			MaxMaxTx:    10_000,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Addr:    "0.0.0.0:6061",
		},
	}
}
