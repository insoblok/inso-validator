package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

// Metrics exposes Prometheus-compatible /metrics endpoint for the validator.
type Metrics struct {
	mu sync.RWMutex

	// Consensus
	AttestationsCreated  atomic.Uint64
	AttestationsReceived atomic.Uint64
	AttestationsVerified atomic.Uint64
	AttestationsFailed   atomic.Uint64
	BlocksFinalized      atomic.Uint64

	// Sync
	SyncedBlock          atomic.Uint64
	IsSynced             atomic.Int32 // 1=synced, 0=syncing

	// Reputation / XP
	CurrentXP            atomic.Int64
	SovereigntyScore     atomic.Int64 // stored as score * 10000
	SovereigntyTier      atomic.Int32

	// P2P
	PeerCount            atomic.Int32

	// Staking
	StakedAmountWei      atomic.Int64 // in ETH units for display

	// Slashing
	SlashesReceived      atomic.Uint64
	SlashesReported      atomic.Uint64

	// RPC
	RPCRequests          atomic.Uint64

	logger log.Logger
}

// New creates a new Metrics instance.
func New() *Metrics {
	return &Metrics{
		logger: log.New("module", "metrics"),
	}
}

// Serve starts the metrics HTTP server.
func (m *Metrics) Serve(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", m.handleMetrics)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		synced := m.IsSynced.Load() == 1
		fmt.Fprintf(w, `{"status":"ok","service":"inso-validator","synced":%v,"peers":%d,"timestamp":%d}`,
			synced, m.PeerCount.Load(), time.Now().Unix())
	})

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		m.logger.Info("Metrics server starting", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			m.logger.Error("Metrics server error", "err", err)
		}
	}()
}

func (m *Metrics) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	// Consensus
	fmt.Fprintf(w, "# HELP inso_validator_attestations_created_total Attestations created by this validator\n")
	fmt.Fprintf(w, "# TYPE inso_validator_attestations_created_total counter\n")
	fmt.Fprintf(w, "inso_validator_attestations_created_total %d\n\n", m.AttestationsCreated.Load())

	fmt.Fprintf(w, "# HELP inso_validator_attestations_received_total Attestations received from peers\n")
	fmt.Fprintf(w, "# TYPE inso_validator_attestations_received_total counter\n")
	fmt.Fprintf(w, "inso_validator_attestations_received_total %d\n\n", m.AttestationsReceived.Load())

	fmt.Fprintf(w, "# HELP inso_validator_attestations_verified_total Valid attestations verified\n")
	fmt.Fprintf(w, "# TYPE inso_validator_attestations_verified_total counter\n")
	fmt.Fprintf(w, "inso_validator_attestations_verified_total %d\n\n", m.AttestationsVerified.Load())

	fmt.Fprintf(w, "# HELP inso_validator_attestations_failed_total Invalid attestations rejected\n")
	fmt.Fprintf(w, "# TYPE inso_validator_attestations_failed_total counter\n")
	fmt.Fprintf(w, "inso_validator_attestations_failed_total %d\n\n", m.AttestationsFailed.Load())

	fmt.Fprintf(w, "# HELP inso_validator_blocks_finalized_total Blocks reaching consensus finality\n")
	fmt.Fprintf(w, "# TYPE inso_validator_blocks_finalized_total counter\n")
	fmt.Fprintf(w, "inso_validator_blocks_finalized_total %d\n\n", m.BlocksFinalized.Load())

	// Sync
	fmt.Fprintf(w, "# HELP inso_validator_synced_block Latest synced block number\n")
	fmt.Fprintf(w, "# TYPE inso_validator_synced_block gauge\n")
	fmt.Fprintf(w, "inso_validator_synced_block %d\n\n", m.SyncedBlock.Load())

	fmt.Fprintf(w, "# HELP inso_validator_is_synced Whether the validator is fully synced (1=yes)\n")
	fmt.Fprintf(w, "# TYPE inso_validator_is_synced gauge\n")
	fmt.Fprintf(w, "inso_validator_is_synced %d\n\n", m.IsSynced.Load())

	// Reputation
	fmt.Fprintf(w, "# HELP inso_validator_xp Current XP points\n")
	fmt.Fprintf(w, "# TYPE inso_validator_xp gauge\n")
	fmt.Fprintf(w, "inso_validator_xp %d\n\n", m.CurrentXP.Load())

	fmt.Fprintf(w, "# HELP inso_validator_sovereignty_score Sovereignty score (x10000 for precision)\n")
	fmt.Fprintf(w, "# TYPE inso_validator_sovereignty_score gauge\n")
	fmt.Fprintf(w, "inso_validator_sovereignty_score %d\n\n", m.SovereigntyScore.Load())

	fmt.Fprintf(w, "# HELP inso_validator_sovereignty_tier Sovereignty tier (0=None,1=Bronze,2=Silver,3=Gold,4=Platinum)\n")
	fmt.Fprintf(w, "# TYPE inso_validator_sovereignty_tier gauge\n")
	fmt.Fprintf(w, "inso_validator_sovereignty_tier %d\n\n", m.SovereigntyTier.Load())

	// P2P
	fmt.Fprintf(w, "# HELP inso_validator_peer_count Connected P2P peers\n")
	fmt.Fprintf(w, "# TYPE inso_validator_peer_count gauge\n")
	fmt.Fprintf(w, "inso_validator_peer_count %d\n\n", m.PeerCount.Load())

	// Staking
	fmt.Fprintf(w, "# HELP inso_validator_staked_eth Total staked in ETH\n")
	fmt.Fprintf(w, "# TYPE inso_validator_staked_eth gauge\n")
	fmt.Fprintf(w, "inso_validator_staked_eth %d\n\n", m.StakedAmountWei.Load())

	// Slashing
	fmt.Fprintf(w, "# HELP inso_validator_slashes_received_total Slashes received\n")
	fmt.Fprintf(w, "# TYPE inso_validator_slashes_received_total counter\n")
	fmt.Fprintf(w, "inso_validator_slashes_received_total %d\n\n", m.SlashesReceived.Load())

	fmt.Fprintf(w, "# HELP inso_validator_slashes_reported_total Slashes reported against others\n")
	fmt.Fprintf(w, "# TYPE inso_validator_slashes_reported_total counter\n")
	fmt.Fprintf(w, "inso_validator_slashes_reported_total %d\n\n", m.SlashesReported.Load())

	// RPC
	fmt.Fprintf(w, "# HELP inso_validator_rpc_requests_total Total RPC requests\n")
	fmt.Fprintf(w, "# TYPE inso_validator_rpc_requests_total counter\n")
	fmt.Fprintf(w, "inso_validator_rpc_requests_total %d\n\n", m.RPCRequests.Load())
}
