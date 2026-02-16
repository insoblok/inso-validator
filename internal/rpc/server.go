package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/ethereum/go-ethereum/common"

	"github.com/insoblok/inso-validator/internal/config"
	"github.com/insoblok/inso-validator/internal/consensus"
	"github.com/insoblok/inso-validator/internal/metrics"
	"github.com/insoblok/inso-validator/internal/p2p"
	"github.com/insoblok/inso-validator/internal/reputation"
	"github.com/insoblok/inso-validator/internal/staking"
	syncEngine "github.com/insoblok/inso-validator/internal/sync"
	"github.com/insoblok/inso-validator/internal/verification"
)

// Server provides the validator's JSON-RPC API.
type Server struct {
	httpServer   *http.Server
	consensus    *consensus.Engine
	sync         *syncEngine.Engine
	verification *verification.Engine
	staking      *staking.Manager
	reputation   *reputation.Manager
	network      *p2p.Network
	logger       log.Logger
	cfg          *config.ValidatorConfig
	seqCfg       *config.SequencerConfig
	metrics      *metrics.Metrics
}

// NewServer creates a new validator RPC server.
func NewServer(
	cfg *config.ValidatorConfig,
	consensus *consensus.Engine,
	sync *syncEngine.Engine,
	verification *verification.Engine,
	staking *staking.Manager,
	network *p2p.Network,
) *Server {
	return &Server{
		consensus:    consensus,
		sync:         sync,
		verification: verification,
		staking:      staking,
		network:      network,
		logger:       log.New("module", "rpc"),
		cfg:          cfg,
	}
}

// SetReputation wires the reputation manager into the RPC server.
func (s *Server) SetReputation(r *reputation.Manager) {
	s.reputation = r
}

// SetSequencerConfig sets the sequencer config for verification calls.
func (s *Server) SetSequencerConfig(cfg *config.SequencerConfig) {
	s.seqCfg = cfg
}

// SetMetrics wires the metrics collector into the RPC server.
func (s *Server) SetMetrics(m *metrics.Metrics) {
	s.metrics = m
}

// Start begins the RPC server.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)

	s.httpServer = &http.Server{
		Addr:        s.cfg.ListenAddr,
		Handler:     mux,
		ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	go func() {
		s.logger.Info("Validator RPC server starting", "addr", s.cfg.ListenAddr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("RPC server error", "err", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the RPC server.
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if s.metrics != nil {
		s.metrics.RPCRequests.Add(1)
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		s.writeError(w, nil, -32700, "parse error")
		return
	}
	defer r.Body.Close()

	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		ID      interface{}     `json:"id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, nil, -32700, "parse error")
		return
	}

	var result interface{}

	switch req.Method {
	case "inso_validatorStatus":
		result = map[string]interface{}{
			"synced":          s.sync.IsSynced(),
			"latestBlock":     s.sync.LatestBlock(),
			"validatorCount":  s.consensus.ValidatorCount(),
			"peerCount":       s.network.PeerCount(),
			"totalStaked":     s.staking.TotalStaked().String(),
		}
	case "inso_getValidators":
		result = s.consensus.Validators()
	case "inso_getPeers":
		result = s.network.Peers()
	case "inso_getActiveStakes":
		result = s.staking.ActiveValidators()

	// ── Verification endpoints ──────────────────────────────────────────
	case "inso_verifyBlock":
		blockNum, err := s.parseUint64Param(req.Params, 0)
		if err != nil {
			s.writeError(w, req.ID, -32602, "invalid block number: "+err.Error())
			return
		}
		block := s.sync.GetBlock(blockNum)
		if block == nil {
			s.writeError(w, req.ID, -32602, "block not found")
			return
		}
		result = s.verification.VerifyBlock(block)

	case "inso_getLaneVerification":
		seqURL := s.sequencerURL()
		result = s.verification.VerifyLaneAllocation(seqURL)

	case "inso_getReceiptVerification":
		txHash, err := s.parseStringParam(req.Params, 0)
		if err != nil {
			s.writeError(w, req.ID, -32602, "invalid txHash: "+err.Error())
			return
		}
		seqURL := s.sequencerURL()
		result = s.verification.VerifyComputeReceipt(seqURL, txHash)

	case "inso_getAdaptiveVerification":
		blockNum, err := s.parseUint64Param(req.Params, 0)
		if err != nil {
			s.writeError(w, req.ID, -32602, "invalid block number: "+err.Error())
			return
		}
		block := s.sync.GetBlock(blockNum)
		if block == nil {
			s.writeError(w, req.ID, -32602, "block not found")
			return
		}
		bounds := verification.DefaultAdaptiveBounds()
		result = s.verification.VerifyAdaptiveBlock(block, bounds)

	// ── Reputation endpoints ────────────────────────────────────────────
	case "inso_getReputationScores":
		if s.reputation == nil {
			s.writeError(w, req.ID, -32603, "reputation manager not available")
			return
		}
		result = s.reputation.AllReputations()

	case "inso_getReputation":
		if s.reputation == nil {
			s.writeError(w, req.ID, -32603, "reputation manager not available")
			return
		}
		addrStr, err := s.parseStringParam(req.Params, 0)
		if err != nil {
			s.writeError(w, req.ID, -32602, "invalid address: "+err.Error())
			return
		}
		addr := common.HexToAddress(addrStr)
		rep := s.reputation.GetReputation(addr)
		if rep == nil {
			s.writeError(w, req.ID, -32602, "validator not found")
			return
		}
		result = rep

	// ── Sync endpoints ──────────────────────────────────────────────────
	case "inso_getSyncStatus":
		result = map[string]interface{}{
			"synced":      s.sync.IsSynced(),
			"latestBlock": s.sync.LatestBlock(),
		}

	default:
		s.writeError(w, req.ID, -32601, "method not found")
		return
	}

	encoded, _ := json.Marshal(result)
	raw := json.RawMessage(encoded)
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  &raw,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"service":        "inso-validator",
		"version":        "phase5",
		"synced":         s.sync.IsSynced(),
		"peers":          s.network.PeerCount(),
		"validatorCount": s.consensus.ValidatorCount(),
	})
}

// handleReady returns 200 only when the validator is synced and has peers.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	synced := s.sync.IsSynced()
	peers := s.network.PeerCount()

	if !synced {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "not_ready",
			"service": "inso-validator",
			"reason":  "not synced",
			"synced":  false,
			"peers":   peers,
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ready",
		"service": "inso-validator",
		"synced":  true,
		"peers":   peers,
	})
}

func (s *Server) writeError(w http.ResponseWriter, id interface{}, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]interface{}{"code": code, "message": msg},
	})
}

// ── Parameter Parsing Helpers ───────────────────────────────────────────────

// parseUint64Param extracts a uint64 from the params array at the given index.
func (s *Server) parseUint64Param(params json.RawMessage, idx int) (uint64, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(params, &arr); err != nil {
		return 0, fmt.Errorf("params must be an array")
	}
	if idx >= len(arr) {
		return 0, fmt.Errorf("missing parameter at index %d", idx)
	}

	var val uint64
	if err := json.Unmarshal(arr[idx], &val); err != nil {
		// Try hex string (e.g. "0x1a")
		var hexStr string
		if err2 := json.Unmarshal(arr[idx], &hexStr); err2 == nil {
			n := new(big.Int)
			if _, ok := n.SetString(strings.TrimPrefix(hexStr, "0x"), 16); ok {
				return n.Uint64(), nil
			}
		}
		return 0, fmt.Errorf("invalid uint64 at index %d", idx)
	}
	return val, nil
}

// parseStringParam extracts a string from the params array at the given index.
func (s *Server) parseStringParam(params json.RawMessage, idx int) (string, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(params, &arr); err != nil {
		return "", fmt.Errorf("params must be an array")
	}
	if idx >= len(arr) {
		return "", fmt.Errorf("missing parameter at index %d", idx)
	}
	var val string
	if err := json.Unmarshal(arr[idx], &val); err != nil {
		return "", fmt.Errorf("invalid string at index %d", idx)
	}
	return val, nil
}

// sequencerURL returns the sequencer RPC URL from configuration or a default.
func (s *Server) sequencerURL() string {
	if s.seqCfg != nil && s.seqCfg.RPCURL != "" {
		return s.seqCfg.RPCURL
	}
	return "http://localhost:8545"
}
