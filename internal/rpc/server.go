package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-validator/internal/config"
	"github.com/insoblok/inso-validator/internal/consensus"
	"github.com/insoblok/inso-validator/internal/p2p"
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
	network      *p2p.Network
	logger       log.Logger
	cfg          *config.ValidatorConfig
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

// Start begins the RPC server.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	mux.HandleFunc("/health", s.handleHealth)

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
		"status":  "ok",
		"service": "inso-validator",
		"synced":  s.sync.IsSynced(),
		"peers":   s.network.PeerCount(),
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
