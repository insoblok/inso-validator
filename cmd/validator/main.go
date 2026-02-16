package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-validator/internal/config"
	"github.com/insoblok/inso-validator/internal/consensus"
	"github.com/insoblok/inso-validator/internal/metrics"
	"github.com/insoblok/inso-validator/internal/p2p"
	"github.com/insoblok/inso-validator/internal/reputation"
	"github.com/insoblok/inso-validator/internal/rpc"
	"github.com/insoblok/inso-validator/internal/staking"
	syncEngine "github.com/insoblok/inso-validator/internal/sync"
	"github.com/insoblok/inso-validator/internal/verification"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	// Setup structured logging
	handler := log.NewTerminalHandler(os.Stdout, true)
	log.SetDefault(log.NewLogger(handler))

	logger := log.New("module", "main")
	logger.Info("InSo Validator starting", "version", version)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("Failed to load config", "err", err)
		os.Exit(1)
	}

	// Validator private key: from env or default devnet key
	// INSO_VALIDATOR_KEY should be a hex-encoded secp256k1 private key (with or without 0x prefix)
	var validatorKey *ecdsa.PrivateKey
	keyHex := os.Getenv("INSO_VALIDATOR_KEY")
	if keyHex == "" {
		// Default Anvil account #2 key for devnet
		keyHex = "7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6"
	}
	keyHex = strings.TrimPrefix(keyHex, "0x")
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		logger.Error("Invalid validator private key hex", "err", err)
		os.Exit(1)
	}
	validatorKey, err = crypto.ToECDSA(keyBytes)
	if err != nil {
		logger.Error("Failed to parse validator private key", "err", err)
		os.Exit(1)
	}
	validatorAddr := crypto.PubkeyToAddress(validatorKey.PublicKey)
	logger.Info("Validator identity loaded", "address", validatorAddr.Hex())

	// Initialize P2P network
	network := p2p.NewNetwork(&cfg.Validator)

	// Initialize sync engine (now takes P2P network for gossip-based sync)
	syncEng := syncEngine.NewEngine(&cfg.Sequencer, network)

	// Initialize verification engine
	verifyEng := verification.NewEngine()

	// Phase 4: Initialize reputation (XP) manager
	repCfg := &reputation.XPConfig{
		MaxXP:              100_000,
		BlockAttestationXP: cfg.Sovereignty.AttestationXP,
		UptimeBonusXP:      cfg.Sovereignty.UptimeBonusXP,
		SlashPenaltyXP:     cfg.Sovereignty.SlashPenaltyXP,
		DecayRatePerDay:    cfg.Sovereignty.XPDecayRate,
	}
	repMgr := reputation.NewManager(repCfg)
	repMgr.Register(validatorAddr)
	logger.Info("Reputation manager initialized",
		"sovereigntyEnabled", cfg.Sovereignty.Enabled,
		"attestationXP", cfg.Sovereignty.AttestationXP,
	)

	// Initialize consensus engine (now takes reputation manager + ECDSA key)
	consensusEng := consensus.NewEngine(&cfg.Consensus, network, validatorAddr, repMgr, validatorKey)

	// Initialize staking manager
	stakingMgr := staking.NewManager(cfg.Validator.MinStake)

	// Register this validator with initial stake
	initialStake := new(big.Int).Mul(
		big.NewInt(int64(cfg.Validator.MinStake)),
		big.NewInt(1e18),
	)
	if err := stakingMgr.Register(validatorAddr, initialStake); err != nil {
		logger.Error("Failed to register validator", "err", err)
		os.Exit(1)
	}

	// Register in consensus engine
	consensusEng.RegisterValidator(validatorAddr, initialStake, 0.8) // initial tasteScore

	// Wire up: verify + attest each new block from sync
	adaptiveBounds := verification.AdaptiveBlockBounds{
		MinGasLimit: cfg.AdaptiveBlock.MinGasLimit,
		MaxGasLimit: cfg.AdaptiveBlock.MaxGasLimit,
		MinMaxTx:    cfg.AdaptiveBlock.MinMaxTx,
		MaxMaxTx:    cfg.AdaptiveBlock.MaxMaxTx,
	}
	if !cfg.AdaptiveBlock.Enabled {
		adaptiveBounds = verification.DefaultAdaptiveBounds()
	}

	syncEng.OnNewBlock(func(block *syncEngine.L2Block) {
		// Verify state root
		result := verifyEng.VerifyBlock(block)
		if !result.Valid {
			logger.Warn("Block verification failed",
				"number", block.Number,
				"error", result.Error,
			)
			return
		}

		// Verify adaptive block bounds
		adaptiveResult := verifyEng.VerifyAdaptiveBlock(block, adaptiveBounds)
		if !adaptiveResult.Valid {
			logger.Warn("Adaptive block check failed",
				"number", block.Number,
				"error", adaptiveResult.Error,
			)
			// Log but don't reject — adaptive bounds may be lenient on devnet
		}

		// Attest the block
		blockHash := common.HexToHash(block.Hash)
		consensusEng.AttestBlock(block.Number, blockHash)
	})

	// Initialize RPC server
	rpcServer := rpc.NewServer(&cfg.Validator, consensusEng, syncEng, verifyEng, stakingMgr, network)

	// Start all services
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start P2P network
	if err := network.Start(ctx); err != nil {
		logger.Error("Failed to start P2P network", "err", err)
		os.Exit(1)
	}

	// Start sync engine
	syncEng.Start(ctx)
	logger.Info("Sync engine started", "sequencer", cfg.Sequencer.RPCURL)

	// Start consensus engine
	consensusEng.Start(ctx)
	logger.Info("Consensus engine started")

	// Start RPC server
	if err := rpcServer.Start(ctx); err != nil {
		logger.Error("Failed to start RPC server", "err", err)
		os.Exit(1)
	}

	// Start Prometheus metrics endpoint
	met := metrics.New()
	met.Serve(":6061")
	logger.Info("Metrics server started", "addr", ":6061")

	// Wire metrics into all components
	syncEng.SetMetrics(met)
	verifyEng.SetMetrics(met)
	consensusEng.SetMetrics(met)
	rpcServer.SetMetrics(met)

	// Seed initial metrics values
	met.PeerCount.Store(int32(network.PeerCount()))
	met.StakedAmountWei.Store(int64(cfg.Validator.MinStake))

	fmt.Println()
	logger.Info("═══════════════════════════════════════════════")
	logger.Info("  InSo Validator is running")
	logger.Info("  RPC: " + cfg.Validator.ListenAddr)
	logger.Info("  P2P Port: " + fmt.Sprintf("%d", cfg.Validator.P2PPort))
	logger.Info("  Validator: " + validatorAddr.Hex())
	logger.Info("  Min Stake: " + fmt.Sprintf("%d INSO", cfg.Validator.MinStake))
	logger.Info("═══════════════════════════════════════════════")
	fmt.Println()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("Received shutdown signal", "signal", sig)

	// Graceful shutdown
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	network.Stop()
	syncEng.Stop()
	consensusEng.Stop()
	if err := rpcServer.Stop(shutdownCtx); err != nil {
		logger.Error("Error during shutdown", "err", err)
	}

	logger.Info("InSo Validator stopped gracefully")
}
