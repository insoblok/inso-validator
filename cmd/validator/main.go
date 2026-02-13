package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-validator/internal/config"
	"github.com/insoblok/inso-validator/internal/consensus"
	"github.com/insoblok/inso-validator/internal/p2p"
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

	// Validator address (in production, derived from keyfile)
	validatorAddr := common.HexToAddress("0x90F79bf6EB2c4f870365E785982E1f101E93b906")

	// Initialize P2P network
	network := p2p.NewNetwork(&cfg.Validator)

	// Initialize sync engine
	syncEng := syncEngine.NewEngine(&cfg.Sequencer)

	// Initialize verification engine
	verifyEng := verification.NewEngine()

	// Initialize consensus engine
	consensusEng := consensus.NewEngine(&cfg.Consensus, network, validatorAddr)

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
	syncEng.OnNewBlock(func(block *syncEngine.L2Block) {
		result := verifyEng.VerifyBlock(block)
		if result.Valid {
			blockHash := common.HexToHash(block.Hash)
			consensusEng.AttestBlock(block.Number, blockHash)
		} else {
			logger.Warn("Block verification failed",
				"number", block.Number,
				"error", result.Error,
			)
		}
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
