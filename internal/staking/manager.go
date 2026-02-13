package staking

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

// Stake represents a validator's staking position.
type Stake struct {
	Validator  common.Address `json:"validator"`
	Amount     *big.Int       `json:"amount"`
	StakedAt   time.Time      `json:"stakedAt"`
	IsActive   bool           `json:"isActive"`
	Rewards    *big.Int       `json:"rewards"`
	SlashCount int            `json:"slashCount"`
}

// Delegation represents a delegator's stake to a validator.
type Delegation struct {
	Delegator  common.Address `json:"delegator"`
	Validator  common.Address `json:"validator"`
	Amount     *big.Int       `json:"amount"`
	DelegatedAt time.Time     `json:"delegatedAt"`
}

// Manager handles staking, delegation, and slashing logic.
type Manager struct {
	mu          sync.RWMutex
	stakes      map[common.Address]*Stake
	delegations map[common.Address][]*Delegation
	minStake    *big.Int
	logger      log.Logger
}

// NewManager creates a new staking manager.
func NewManager(minStakeINSO uint64) *Manager {
	minStake := new(big.Int).Mul(
		new(big.Int).SetUint64(minStakeINSO),
		big.NewInt(1e18),
	)

	return &Manager{
		stakes:      make(map[common.Address]*Stake),
		delegations: make(map[common.Address][]*Delegation),
		minStake:    minStake,
		logger:      log.New("module", "staking"),
	}
}

// Register creates a new staking position for a validator.
func (m *Manager) Register(validator common.Address, amount *big.Int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if amount.Cmp(m.minStake) < 0 {
		return fmt.Errorf("stake %s below minimum %s", amount.String(), m.minStake.String())
	}

	if _, exists := m.stakes[validator]; exists {
		return fmt.Errorf("validator %s already registered", validator.Hex())
	}

	m.stakes[validator] = &Stake{
		Validator: validator,
		Amount:    new(big.Int).Set(amount),
		StakedAt:  time.Now(),
		IsActive:  true,
		Rewards:   big.NewInt(0),
	}

	m.logger.Info("Validator staked",
		"validator", validator.Hex(),
		"amount", formatINSO(amount),
	)
	return nil
}

// Delegate adds delegation to a validator.
func (m *Manager) Delegate(delegator, validator common.Address, amount *big.Int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	stake, exists := m.stakes[validator]
	if !exists || !stake.IsActive {
		return fmt.Errorf("validator %s not active", validator.Hex())
	}

	m.delegations[validator] = append(m.delegations[validator], &Delegation{
		Delegator:   delegator,
		Validator:   validator,
		Amount:      new(big.Int).Set(amount),
		DelegatedAt: time.Now(),
	})

	// Add delegation to validator's total stake
	stake.Amount.Add(stake.Amount, amount)

	m.logger.Info("Delegation added",
		"delegator", delegator.Hex(),
		"validator", validator.Hex(),
		"amount", formatINSO(amount),
	)
	return nil
}

// Slash penalizes a validator for misbehavior.
func (m *Manager) Slash(validator common.Address, percentage float64, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	stake, exists := m.stakes[validator]
	if !exists {
		return fmt.Errorf("validator %s not found", validator.Hex())
	}

	// Calculate slash amount
	slashAmount := new(big.Float).Mul(
		new(big.Float).SetInt(stake.Amount),
		new(big.Float).SetFloat64(percentage/100.0),
	)
	slashInt, _ := slashAmount.Int(nil)

	stake.Amount.Sub(stake.Amount, slashInt)
	stake.SlashCount++

	// Deactivate if below minimum
	if stake.Amount.Cmp(m.minStake) < 0 {
		stake.IsActive = false
	}

	m.logger.Warn("Validator slashed",
		"validator", validator.Hex(),
		"amount", formatINSO(slashInt),
		"reason", reason,
		"remaining", formatINSO(stake.Amount),
		"slashCount", stake.SlashCount,
	)
	return nil
}

// GetStake returns a validator's staking info.
func (m *Manager) GetStake(validator common.Address) *Stake {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stakes[validator]
}

// TotalStaked returns the total amount staked across all validators.
func (m *Manager) TotalStaked() *big.Int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := big.NewInt(0)
	for _, s := range m.stakes {
		if s.IsActive {
			total.Add(total, s.Amount)
		}
	}
	return total
}

// ActiveValidators returns all active validators.
func (m *Manager) ActiveValidators() []*Stake {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var active []*Stake
	for _, s := range m.stakes {
		if s.IsActive {
			active = append(active, s)
		}
	}
	return active
}

func formatINSO(wei *big.Int) string {
	inso := new(big.Float).Quo(
		new(big.Float).SetInt(wei),
		new(big.Float).SetFloat64(1e18),
	)
	return fmt.Sprintf("%.2f INSO", inso)
}
