package staking

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

var (
	addrA = common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	addrB = common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	addrC = common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")
)

// inso converts an INSO amount to wei.
func inso(n int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(n), big.NewInt(1e18))
}

// ── NewManager ───────────────────────────────────────────────────────────────

func TestNewManager(t *testing.T) {
	m := NewManager(100)
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.minStake.Cmp(inso(100)) != 0 {
		t.Errorf("expected minStake 100e18, got %s", m.minStake.String())
	}
}

// ── Register ─────────────────────────────────────────────────────────────────

func TestRegisterSuccess(t *testing.T) {
	m := NewManager(100)
	err := m.Register(addrA, inso(200))
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	s := m.GetStake(addrA)
	if s == nil {
		t.Fatal("stake not found after register")
	}
	if !s.IsActive {
		t.Error("validator should be active")
	}
	if s.Amount.Cmp(inso(200)) != 0 {
		t.Errorf("expected 200 INSO, got %s", s.Amount.String())
	}
}

func TestRegisterBelowMinimum(t *testing.T) {
	m := NewManager(100)
	err := m.Register(addrA, inso(50))
	if err == nil {
		t.Error("expected error for stake below minimum")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(200))
	err := m.Register(addrA, inso(300))
	if err == nil {
		t.Error("expected error for duplicate registration")
	}
}

func TestRegisterMultiple(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(200))
	_ = m.Register(addrB, inso(300))

	if m.GetStake(addrA) == nil || m.GetStake(addrB) == nil {
		t.Error("both validators should be registered")
	}
}

// ── Delegate ─────────────────────────────────────────────────────────────────

func TestDelegateSuccess(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(200))

	err := m.Delegate(addrB, addrA, inso(50))
	if err != nil {
		t.Fatalf("Delegate failed: %v", err)
	}

	// Delegation should increase validator's total stake
	s := m.GetStake(addrA)
	if s.Amount.Cmp(inso(250)) != 0 {
		t.Errorf("expected 250 INSO after delegation, got %s", s.Amount.String())
	}
}

func TestDelegateToInactiveValidator(t *testing.T) {
	m := NewManager(100)
	err := m.Delegate(addrB, addrA, inso(50))
	if err == nil {
		t.Error("expected error when delegating to non-existent validator")
	}
}

func TestMultipleDelegations(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(200))

	_ = m.Delegate(addrB, addrA, inso(30))
	_ = m.Delegate(addrC, addrA, inso(20))

	s := m.GetStake(addrA)
	if s.Amount.Cmp(inso(250)) != 0 {
		t.Errorf("expected 250 INSO after two delegations, got %s", s.Amount.String())
	}
}

// ── Slash ────────────────────────────────────────────────────────────────────

func TestSlashReducesStake(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(200))

	err := m.Slash(addrA, 10.0, "double signing")
	if err != nil {
		t.Fatalf("Slash failed: %v", err)
	}

	s := m.GetStake(addrA)
	// big.Float(200e18) * 0.10 may have minor rounding; check within tolerance
	expected := inso(180)
	diff := new(big.Int).Abs(new(big.Int).Sub(s.Amount, expected))
	tolerance := big.NewInt(1e12) // 1e-6 INSO tolerance
	if diff.Cmp(tolerance) > 0 {
		t.Errorf("expected ~180 INSO after 10%% slash, got %s (diff %s)", s.Amount.String(), diff.String())
	}
	if s.SlashCount != 1 {
		t.Errorf("expected slashCount=1, got %d", s.SlashCount)
	}
}

func TestSlashDeactivatesBelowMinimum(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(110))

	// 50% slash: 110 -> 55, below minStake of 100 — should deactivate
	err := m.Slash(addrA, 50.0, "critical violation")
	if err != nil {
		t.Fatalf("Slash failed: %v", err)
	}

	s := m.GetStake(addrA)
	if s.IsActive {
		t.Error("validator should be deactivated after dropping below minimum")
	}
}

func TestSlashNonExistentValidator(t *testing.T) {
	m := NewManager(100)
	err := m.Slash(addrA, 10.0, "reason")
	if err == nil {
		t.Error("expected error slashing non-existent validator")
	}
}

func TestSlashIncrementsCount(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(1000))

	_ = m.Slash(addrA, 1.0, "first")
	_ = m.Slash(addrA, 1.0, "second")
	_ = m.Slash(addrA, 1.0, "third")

	s := m.GetStake(addrA)
	if s.SlashCount != 3 {
		t.Errorf("expected slashCount=3, got %d", s.SlashCount)
	}
}

// ── TotalStaked ──────────────────────────────────────────────────────────────

func TestTotalStakedEmpty(t *testing.T) {
	m := NewManager(100)
	if m.TotalStaked().Sign() != 0 {
		t.Error("expected 0 total staked initially")
	}
}

func TestTotalStakedMultiple(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(200))
	_ = m.Register(addrB, inso(300))

	expected := inso(500)
	if m.TotalStaked().Cmp(expected) != 0 {
		t.Errorf("expected 500 INSO total, got %s", m.TotalStaked().String())
	}
}

func TestTotalStakedExcludesInactive(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(110))
	_ = m.Register(addrB, inso(300))

	// Slash addrA below minimum to deactivate
	_ = m.Slash(addrA, 50.0, "penalize")

	// Only addrB should count
	if m.TotalStaked().Cmp(inso(300)) != 0 {
		t.Errorf("expected 300 INSO (only active), got %s", m.TotalStaked().String())
	}
}

// ── ActiveValidators ─────────────────────────────────────────────────────────

func TestActiveValidatorsEmpty(t *testing.T) {
	m := NewManager(100)
	if len(m.ActiveValidators()) != 0 {
		t.Error("expected 0 active validators initially")
	}
}

func TestActiveValidatorsCount(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(200))
	_ = m.Register(addrB, inso(300))

	if len(m.ActiveValidators()) != 2 {
		t.Errorf("expected 2 active, got %d", len(m.ActiveValidators()))
	}
}

func TestActiveValidatorsExcludesSlashed(t *testing.T) {
	m := NewManager(100)
	_ = m.Register(addrA, inso(110))
	_ = m.Register(addrB, inso(300))

	// Deactivate addrA
	_ = m.Slash(addrA, 50.0, "reason")

	active := m.ActiveValidators()
	if len(active) != 1 {
		t.Fatalf("expected 1 active, got %d", len(active))
	}
	if active[0].Validator != addrB {
		t.Errorf("expected addrB to be the active validator")
	}
}

// ── GetStake ─────────────────────────────────────────────────────────────────

func TestGetStakeNotFound(t *testing.T) {
	m := NewManager(100)
	if m.GetStake(addrA) != nil {
		t.Error("expected nil for non-existent validator")
	}
}
