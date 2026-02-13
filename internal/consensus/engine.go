package consensus

import (
	"context"
	"crypto/sha256"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-validator/internal/config"
	"github.com/insoblok/inso-validator/internal/p2p"
)

// Attestation is a validator's signed approval of a block.
type Attestation struct {
	BlockNumber uint64         `json:"blockNumber"`
	BlockHash   common.Hash    `json:"blockHash"`
	Validator   common.Address `json:"validator"`
	TasteScore  float64        `json:"tasteScore"`
	Signature   []byte         `json:"signature"`
	Timestamp   time.Time      `json:"timestamp"`
}

// ValidatorInfo tracks a registered validator's state.
type ValidatorInfo struct {
	Address    common.Address `json:"address"`
	Stake      *big.Int       `json:"stake"`
	TasteScore float64        `json:"tasteScore"`
	VotePower  float64        `json:"votePower"` // stake * tasteScore weight
	IsActive   bool           `json:"isActive"`
	SlashCount int            `json:"slashCount"`
	JoinedAt   time.Time      `json:"joinedAt"`
}

// Engine implements TasteScore-weighted Proof-of-Stake consensus.
// Voting power = stake * (1 + tasteScoreBonus).
type Engine struct {
	mu sync.RWMutex

	cfg           *config.ConsensusConfig
	network       *p2p.Network
	validators    map[common.Address]*ValidatorInfo
	attestations  map[uint64][]*Attestation // blockNum -> attestations
	localAddr     common.Address
	logger        log.Logger
	cancel        context.CancelFunc
}

// NewEngine creates a new consensus engine.
func NewEngine(cfg *config.ConsensusConfig, network *p2p.Network, localAddr common.Address) *Engine {
	return &Engine{
		cfg:          cfg,
		network:      network,
		validators:   make(map[common.Address]*ValidatorInfo),
		attestations: make(map[uint64][]*Attestation),
		localAddr:    localAddr,
		logger:       log.New("module", "consensus"),
	}
}

// Start begins the consensus engine.
func (e *Engine) Start(ctx context.Context) {
	ctx, e.cancel = context.WithCancel(ctx)

	e.logger.Info("Consensus engine started",
		"blockTimeout", e.cfg.BlockTimeout,
		"attestationDelay", e.cfg.AttestationDelay,
		"slashingEnabled", e.cfg.SlashingEnabled,
	)

	go e.processMessages(ctx)
}

// Stop halts the consensus engine.
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
}

// RegisterValidator adds a validator to the active set.
func (e *Engine) RegisterValidator(addr common.Address, stake *big.Int, tasteScore float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// VotePower = stake_in_ether * (1 + 0.5 * tasteScore)
	stakeFloat := new(big.Float).Quo(
		new(big.Float).SetInt(stake),
		new(big.Float).SetFloat64(1e18),
	)
	stakeF64, _ := stakeFloat.Float64()
	votePower := stakeF64 * (1 + 0.5*tasteScore)

	e.validators[addr] = &ValidatorInfo{
		Address:    addr,
		Stake:      stake,
		TasteScore: tasteScore,
		VotePower:  votePower,
		IsActive:   true,
		JoinedAt:   time.Now(),
	}

	e.logger.Info("Validator registered",
		"addr", addr.Hex(),
		"stake", stakeF64,
		"tasteScore", tasteScore,
		"votePower", votePower,
	)
}

// AttestBlock creates and broadcasts an attestation for a block.
func (e *Engine) AttestBlock(blockNum uint64, blockHash common.Hash) (*Attestation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Create attestation
	att := &Attestation{
		BlockNumber: blockNum,
		BlockHash:   blockHash,
		Validator:   e.localAddr,
		Timestamp:   time.Now(),
	}

	// In production: sign with validator's private key
	att.Signature = e.signAttestation(att)

	// Record locally
	e.attestations[blockNum] = append(e.attestations[blockNum], att)

	// Broadcast to peers
	e.network.Broadcast(p2p.MsgAttestation, att.Signature)

	e.logger.Debug("Block attested",
		"blockNumber", blockNum,
		"blockHash", blockHash.Hex()[:10],
	)

	return att, nil
}

// IsFinalized checks if a block has reached consensus (>2/3 voting power).
func (e *Engine) IsFinalized(blockNum uint64) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	atts := e.attestations[blockNum]
	if len(atts) == 0 {
		return false
	}

	// Sum up voting power of attestors
	var attestedPower float64
	var totalPower float64

	for _, v := range e.validators {
		if v.IsActive {
			totalPower += v.VotePower
		}
	}

	for _, att := range atts {
		if v, ok := e.validators[att.Validator]; ok && v.IsActive {
			attestedPower += v.VotePower
		}
	}

	if totalPower == 0 {
		return false
	}

	return attestedPower/totalPower > 2.0/3.0
}

// ValidatorCount returns the number of active validators.
func (e *Engine) ValidatorCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	count := 0
	for _, v := range e.validators {
		if v.IsActive {
			count++
		}
	}
	return count
}

// Validators returns a list of all registered validators.
func (e *Engine) Validators() []*ValidatorInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	list := make([]*ValidatorInfo, 0, len(e.validators))
	for _, v := range e.validators {
		list = append(list, v)
	}
	return list
}

// signAttestation generates a signature placeholder.
func (e *Engine) signAttestation(att *Attestation) []byte {
	h := sha256.New()
	h.Write(new(big.Int).SetUint64(att.BlockNumber).Bytes())
	h.Write(att.BlockHash.Bytes())
	h.Write(att.Validator.Bytes())
	return h.Sum(nil)
}

func (e *Engine) processMessages(ctx context.Context) {
	msgCh := e.network.Messages()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			switch msg.Type {
			case p2p.MsgAttestation:
				e.handleAttestation(msg)
			case p2p.MsgNewBlock:
				e.handleNewBlock(msg)
			case p2p.MsgSlashReport:
				e.handleSlashReport(msg)
			}
		}
	}
}

func (e *Engine) handleAttestation(msg *p2p.Message) {
	e.logger.Debug("Received attestation", "from", msg.From)
}

func (e *Engine) handleNewBlock(msg *p2p.Message) {
	e.logger.Debug("Received new block", "from", msg.From)
}

func (e *Engine) handleSlashReport(msg *p2p.Message) {
	if !e.cfg.SlashingEnabled {
		return
	}
	e.logger.Warn("Received slash report", "from", msg.From)
}
