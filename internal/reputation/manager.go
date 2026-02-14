package reputation

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

// XPConfig holds configuration for the XP reputation system.
type XPConfig struct {
	MaxXP                uint64  `yaml:"max_xp"`                  // XP cap (default 100,000)
	BlockAttestationXP   uint64  `yaml:"block_attestation_xp"`    // XP per block attestation
	UptimeBonusXP        uint64  `yaml:"uptime_bonus_xp"`         // XP per uptime check
	SlashPenaltyXP       uint64  `yaml:"slash_penalty_xp"`        // XP lost per slash
	DecayRatePerDay      float64 `yaml:"decay_rate_per_day"`      // daily XP decay (0.01 = 1%)
}

// DefaultXPConfig returns sensible defaults.
func DefaultXPConfig() *XPConfig {
	return &XPConfig{
		MaxXP:              100_000,
		BlockAttestationXP: 10,
		UptimeBonusXP:      5,
		SlashPenaltyXP:     500,
		DecayRatePerDay:    0.005, // 0.5% daily decay
	}
}

// ValidatorReputation tracks XP, uptime, and computed sovereignty score.
type ValidatorReputation struct {
	Address          common.Address `json:"address"`
	XP               uint64         `json:"xp"`
	UptimeBps        uint16         `json:"uptimeBps"`        // 0-10000 basis points
	BlocksAttested   uint64         `json:"blocksAttested"`
	BlocksMissed     uint64         `json:"blocksMissed"`
	SlashCount       uint            `json:"slashCount"`
	SovereigntyScore float64        `json:"sovereigntyScore"` // 0.0-1.0
	Tier             uint8          `json:"tier"`             // 0=None,1=Bronze,2=Silver,3=Gold,4=Platinum
	LastActive       time.Time      `json:"lastActive"`
	JoinedAt         time.Time      `json:"joinedAt"`
}

// Manager tracks XP-based reputation for all validators.
type Manager struct {
	mu     sync.RWMutex
	cfg    *XPConfig
	reps   map[common.Address]*ValidatorReputation
	logger log.Logger
}

// NewManager creates a new reputation manager.
func NewManager(cfg *XPConfig) *Manager {
	if cfg == nil {
		cfg = DefaultXPConfig()
	}
	return &Manager{
		cfg:    cfg,
		reps:   make(map[common.Address]*ValidatorReputation),
		logger: log.New("module", "reputation"),
	}
}

// Register initializes reputation tracking for a validator.
func (m *Manager) Register(addr common.Address) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.reps[addr]; exists {
		return
	}

	m.reps[addr] = &ValidatorReputation{
		Address:    addr,
		XP:         0,
		UptimeBps:  10000, // start at 100%
		JoinedAt:   time.Now(),
		LastActive: time.Now(),
	}

	m.logger.Info("Validator reputation initialized", "addr", addr.Hex())
}

// RecordAttestation grants XP for a block attestation.
func (m *Manager) RecordAttestation(addr common.Address) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rep, ok := m.reps[addr]
	if !ok {
		return
	}

	rep.BlocksAttested++
	rep.LastActive = time.Now()
	m.addXP(rep, m.cfg.BlockAttestationXP, "block attestation")
}

// RecordMissedBlock penalizes a validator that didn't attest.
func (m *Manager) RecordMissedBlock(addr common.Address) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rep, ok := m.reps[addr]
	if !ok {
		return
	}

	rep.BlocksMissed++
	m.updateUptime(rep)
}

// RecordSlash applies a slash penalty.
func (m *Manager) RecordSlash(addr common.Address) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rep, ok := m.reps[addr]
	if !ok {
		return
	}

	rep.SlashCount++
	m.removeXP(rep, m.cfg.SlashPenaltyXP, "slash penalty")
}

// RecordUptimeCheck grants a small XP bonus for being online.
func (m *Manager) RecordUptimeCheck(addr common.Address) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rep, ok := m.reps[addr]
	if !ok {
		return
	}

	rep.LastActive = time.Now()
	m.addXP(rep, m.cfg.UptimeBonusXP, "uptime check")
}

// ComputeSovereignty computes the sovereignty score for a validator.
// sovereignty = 0.4*normalizedXP + 0.3*normalizedUptime + 0.2*longevity + 0.1*tasteScoreBonus
// For the validator-local computation, tasteScore is passed in.
func (m *Manager) ComputeSovereignty(addr common.Address, stakeWei float64, tasteScore float64) (float64, uint8) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rep, ok := m.reps[addr]
	if !ok {
		return 0, 0
	}

	// Normalize XP: 0-1
	xpNorm := float64(rep.XP) / float64(m.cfg.MaxXP)
	if xpNorm > 1.0 {
		xpNorm = 1.0
	}

	// Normalize uptime: already 0-1 (bps / 10000)
	uptimeNorm := float64(rep.UptimeBps) / 10000.0

	// Longevity: days since join, capped at 365 days
	daysSinceJoin := time.Since(rep.JoinedAt).Hours() / 24
	longevity := math.Min(daysSinceJoin/365.0, 1.0)

	// TasteScore bonus (0-1)
	tsNorm := tasteScore
	if tsNorm > 1.0 {
		tsNorm = 1.0
	}

	// Weighted sum
	score := 0.4*xpNorm + 0.3*uptimeNorm + 0.2*longevity + 0.1*tsNorm

	// Determine tier
	tier := scoreTier(score)

	rep.SovereigntyScore = score
	rep.Tier = tier

	m.logger.Debug("Sovereignty computed",
		"addr", addr.Hex(),
		"score", fmt.Sprintf("%.4f", score),
		"tier", tierName(tier),
		"xp", rep.XP,
		"uptime", rep.UptimeBps,
	)

	return score, tier
}

// GetReputation returns the reputation for a validator.
func (m *Manager) GetReputation(addr common.Address) *ValidatorReputation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if rep, ok := m.reps[addr]; ok {
		cp := *rep
		return &cp
	}
	return nil
}

// AllReputations returns all validator reputations.
func (m *Manager) AllReputations() []*ValidatorReputation {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*ValidatorReputation, 0, len(m.reps))
	for _, rep := range m.reps {
		cp := *rep
		list = append(list, &cp)
	}
	return list
}

// ApplyDecay reduces all validators' XP by the daily decay rate.
// Should be called once per day (or prorated).
func (m *Manager) ApplyDecay() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, rep := range m.reps {
		decay := uint64(float64(rep.XP) * m.cfg.DecayRatePerDay)
		if decay > rep.XP {
			rep.XP = 0
		} else {
			rep.XP -= decay
		}
	}
}

// --- internal helpers ---

func (m *Manager) addXP(rep *ValidatorReputation, amount uint64, reason string) {
	rep.XP += amount
	if rep.XP > m.cfg.MaxXP {
		rep.XP = m.cfg.MaxXP
	}
	m.logger.Debug("XP granted",
		"addr", rep.Address.Hex(),
		"amount", amount,
		"total", rep.XP,
		"reason", reason,
	)
}

func (m *Manager) removeXP(rep *ValidatorReputation, amount uint64, reason string) {
	if amount >= rep.XP {
		rep.XP = 0
	} else {
		rep.XP -= amount
	}
	m.logger.Debug("XP removed",
		"addr", rep.Address.Hex(),
		"amount", amount,
		"total", rep.XP,
		"reason", reason,
	)
}

func (m *Manager) updateUptime(rep *ValidatorReputation) {
	total := rep.BlocksAttested + rep.BlocksMissed
	if total == 0 {
		rep.UptimeBps = 10000
		return
	}
	rep.UptimeBps = uint16((rep.BlocksAttested * 10000) / total)
}

func scoreTier(score float64) uint8 {
	switch {
	case score >= 0.85:
		return 4 // Platinum
	case score >= 0.65:
		return 3 // Gold
	case score >= 0.40:
		return 2 // Silver
	case score >= 0.15:
		return 1 // Bronze
	default:
		return 0 // None
	}
}

func tierName(tier uint8) string {
	switch tier {
	case 1:
		return "Bronze"
	case 2:
		return "Silver"
	case 3:
		return "Gold"
	case 4:
		return "Platinum"
	default:
		return "None"
	}
}
