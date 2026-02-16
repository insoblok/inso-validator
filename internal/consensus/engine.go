package consensus

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-validator/internal/config"
	"github.com/insoblok/inso-validator/internal/metrics"
	"github.com/insoblok/inso-validator/internal/p2p"
	"github.com/insoblok/inso-validator/internal/reputation"
)

var (
	ErrInvalidSignature = errors.New("invalid attestation signature")
	ErrUnknownValidator = errors.New("unknown validator address")
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
	Address          common.Address `json:"address"`
	Stake            *big.Int       `json:"stake"`
	TasteScore       float64        `json:"tasteScore"`
	VotePower        float64        `json:"votePower"`        // sovereignty-weighted voting power
	SovereigntyScore float64        `json:"sovereigntyScore"` // Phase 4: computed sovereignty
	SovereigntyTier  uint8          `json:"sovereigntyTier"`  // Phase 4: tier
	IsActive         bool           `json:"isActive"`
	SlashCount       int            `json:"slashCount"`
	JoinedAt         time.Time      `json:"joinedAt"`
}

// Engine implements sovereignty-weighted Proof-of-Stake consensus.
// Phase 4: VotingPower = stake * (1 + sovereigntyBonus)
// where sovereigntyBonus = sovereigntyScore * 0.5
// Phase 5: Real ECDSA secp256k1 attestation signatures.
type Engine struct {
	mu sync.RWMutex

	cfg           *config.ConsensusConfig
	network       *p2p.Network
	repMgr        *reputation.Manager
	privateKey    *ecdsa.PrivateKey
	validators    map[common.Address]*ValidatorInfo
	attestations  map[uint64][]*Attestation // blockNum -> attestations
	localAddr     common.Address
	logger        log.Logger
	cancel        context.CancelFunc
	metrics       *metrics.Metrics
}

// NewEngine creates a new consensus engine with an ECDSA private key for signing.
func NewEngine(cfg *config.ConsensusConfig, network *p2p.Network, localAddr common.Address, repMgr *reputation.Manager, privKey *ecdsa.PrivateKey) *Engine {
	return &Engine{
		cfg:          cfg,
		network:      network,
		repMgr:       repMgr,
		privateKey:   privKey,
		validators:   make(map[common.Address]*ValidatorInfo),
		attestations: make(map[uint64][]*Attestation),
		localAddr:    localAddr,
		logger:       log.New("module", "consensus"),
	}
}

// SetMetrics wires the metrics collector into the consensus engine.
func (e *Engine) SetMetrics(m *metrics.Metrics) {
	e.metrics = m
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

	// VotePower = stake_in_ether * (1 + 0.5 * sovereigntyScore)
	// Phase 4: sovereignty score factors in XP, uptime, DID age, TasteScore
	stakeFloat := new(big.Float).Quo(
		new(big.Float).SetInt(stake),
		new(big.Float).SetFloat64(1e18),
	)
	stakeF64, _ := stakeFloat.Float64()

	// Get sovereignty score from reputation manager
	var sovereigntyScore float64
	var tier uint8
	if e.repMgr != nil {
		sovereigntyScore, tier = e.repMgr.ComputeSovereignty(addr, stakeF64, tasteScore)
	}

	// Phase 4: sovereignty-weighted voting power
	votePower := stakeF64 * (1 + 0.5*sovereigntyScore)

	e.validators[addr] = &ValidatorInfo{
		Address:          addr,
		Stake:            stake,
		TasteScore:       tasteScore,
		VotePower:        votePower,
		SovereigntyScore: sovereigntyScore,
		SovereigntyTier:  tier,
		IsActive:         true,
		JoinedAt:         time.Now(),
	}

	e.logger.Info("Validator registered",
		"addr", addr.Hex(),
		"stake", stakeF64,
		"tasteScore", tasteScore,
		"sovereigntyScore", sovereigntyScore,
		"tier", tier,
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

	// Phase 4: record attestation in reputation system for XP
	if e.repMgr != nil {
		e.repMgr.RecordAttestation(e.localAddr)
	}

	// Broadcast to peers
	e.network.Broadcast(p2p.MsgAttestation, att.Signature)

	// Update metrics
	if e.metrics != nil {
		e.metrics.AttestationsCreated.Add(1)
	}

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

	finalized := attestedPower/totalPower > 2.0/3.0
	if finalized && e.metrics != nil {
		e.metrics.BlocksFinalized.Add(1)
	}
	return finalized
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

// attestationHash computes the Keccak-256 digest over the attestation fields.
// The hash is: keccak256(abi.encodePacked(blockNumber, blockHash, validator)).
func attestationHash(att *Attestation) common.Hash {
	data := make([]byte, 0, 8+32+20)
	data = append(data, new(big.Int).SetUint64(att.BlockNumber).Bytes()...)
	data = append(data, att.BlockHash.Bytes()...)
	data = append(data, att.Validator.Bytes()...)
	return crypto.Keccak256Hash(data)
}

// signAttestation signs the attestation with the validator's secp256k1 private key.
func (e *Engine) signAttestation(att *Attestation) []byte {
	hash := attestationHash(att)
	sig, err := crypto.Sign(hash.Bytes(), e.privateKey)
	if err != nil {
		e.logger.Error("Failed to sign attestation", "err", err)
		return nil
	}
	return sig // 65 bytes: R (32) + S (32) + V (1)
}

// VerifyAttestation validates an attestation's ECDSA signature and checks
// that the recovered address matches the claimed validator and is registered.
func (e *Engine) VerifyAttestation(att *Attestation) error {
	if len(att.Signature) != 65 {
		return ErrInvalidSignature
	}

	hash := attestationHash(att)

	// Recover the public key from the signature
	pubKey, err := crypto.SigToPub(hash.Bytes(), att.Signature)
	if err != nil {
		return ErrInvalidSignature
	}

	// Derive the address and compare
	recovered := crypto.PubkeyToAddress(*pubKey)
	if recovered != att.Validator {
		return ErrInvalidSignature
	}

	// Ensure the validator is registered
	e.mu.RLock()
	defer e.mu.RUnlock()
	if _, ok := e.validators[att.Validator]; !ok {
		return ErrUnknownValidator
	}

	return nil
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
