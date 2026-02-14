package reputation

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func TestRegisterAndGetReputation(t *testing.T) {
	m := NewManager(nil)
	addr := common.HexToAddress("0x1234")

	m.Register(addr)
	rep := m.GetReputation(addr)
	if rep == nil {
		t.Fatal("expected reputation to be initialized")
	}
	if rep.XP != 0 {
		t.Fatalf("expected 0 XP, got %d", rep.XP)
	}
	if rep.UptimeBps != 10000 {
		t.Fatalf("expected 10000 uptime bps, got %d", rep.UptimeBps)
	}
}

func TestRecordAttestation(t *testing.T) {
	m := NewManager(nil)
	addr := common.HexToAddress("0x1234")
	m.Register(addr)

	m.RecordAttestation(addr)
	m.RecordAttestation(addr)
	m.RecordAttestation(addr)

	rep := m.GetReputation(addr)
	if rep.XP != 30 { // 3 * 10 XP
		t.Fatalf("expected 30 XP, got %d", rep.XP)
	}
	if rep.BlocksAttested != 3 {
		t.Fatalf("expected 3 blocks attested, got %d", rep.BlocksAttested)
	}
}

func TestRecordSlash(t *testing.T) {
	m := NewManager(nil)
	addr := common.HexToAddress("0x1234")
	m.Register(addr)

	// Grant some XP first
	for i := 0; i < 100; i++ {
		m.RecordAttestation(addr)
	}

	m.RecordSlash(addr)

	rep := m.GetReputation(addr)
	if rep.XP != 500 { // 1000 - 500
		t.Fatalf("expected 500 XP after slash, got %d", rep.XP)
	}
	if rep.SlashCount != 1 {
		t.Fatalf("expected 1 slash, got %d", rep.SlashCount)
	}
}

func TestSlashFloorZero(t *testing.T) {
	m := NewManager(nil)
	addr := common.HexToAddress("0x1234")
	m.Register(addr)

	m.RecordAttestation(addr) // 10 XP
	m.RecordSlash(addr)       // -500, floor at 0

	rep := m.GetReputation(addr)
	if rep.XP != 0 {
		t.Fatalf("expected 0 XP after big slash, got %d", rep.XP)
	}
}

func TestXPCap(t *testing.T) {
	cfg := DefaultXPConfig()
	cfg.BlockAttestationXP = 50000 // large grants
	m := NewManager(cfg)
	addr := common.HexToAddress("0x1234")
	m.Register(addr)

	m.RecordAttestation(addr)
	m.RecordAttestation(addr)
	m.RecordAttestation(addr)

	rep := m.GetReputation(addr)
	if rep.XP != 100000 { // capped at MAX_XP
		t.Fatalf("expected 100000 XP (capped), got %d", rep.XP)
	}
}

func TestMissedBlocksReduceUptime(t *testing.T) {
	m := NewManager(nil)
	addr := common.HexToAddress("0x1234")
	m.Register(addr)

	// Attest 8, miss 2 → 80% uptime
	for i := 0; i < 8; i++ {
		m.RecordAttestation(addr)
	}
	m.RecordMissedBlock(addr)
	m.RecordMissedBlock(addr)

	rep := m.GetReputation(addr)
	if rep.UptimeBps != 8000 { // 8/10 = 80%
		t.Fatalf("expected 8000 uptime bps, got %d", rep.UptimeBps)
	}
}

func TestComputeSovereignty(t *testing.T) {
	m := NewManager(nil)
	addr := common.HexToAddress("0x1234")
	m.Register(addr)

	// Build up XP
	for i := 0; i < 500; i++ {
		m.RecordAttestation(addr)
	}

	score, tier := m.ComputeSovereignty(addr, 100000, 0.8)

	if score <= 0 {
		t.Fatal("expected positive sovereignty score")
	}
	if tier == 0 {
		t.Fatal("expected non-zero tier with 5000 XP and high tasteScore")
	}
}

func TestComputeSovereigntyHighProfile(t *testing.T) {
	cfg := DefaultXPConfig()
	m := NewManager(cfg)
	addr := common.HexToAddress("0x1234")
	m.Register(addr)

	// Max out XP
	m.mu.Lock()
	m.reps[addr].XP = 100000
	m.reps[addr].UptimeBps = 9900
	m.reps[addr].JoinedAt = time.Now().Add(-365 * 24 * time.Hour) // 1 year ago
	m.mu.Unlock()

	score, tier := m.ComputeSovereignty(addr, 500000, 0.95)

	// XP: 1.0 * 0.4 = 0.4
	// Uptime: 0.99 * 0.3 = 0.297
	// Longevity: 1.0 * 0.2 = 0.2
	// TasteScore: 0.95 * 0.1 = 0.095
	// Total ≈ 0.992 → Platinum
	if score < 0.85 {
		t.Fatalf("expected Platinum-level score, got %.4f", score)
	}
	if tier != 4 {
		t.Fatalf("expected tier 4 (Platinum), got %d", tier)
	}
}

func TestApplyDecay(t *testing.T) {
	m := NewManager(nil)
	addr := common.HexToAddress("0x1234")
	m.Register(addr)

	// Grant XP
	for i := 0; i < 100; i++ {
		m.RecordAttestation(addr)
	}
	before := m.GetReputation(addr).XP

	m.ApplyDecay()
	after := m.GetReputation(addr).XP

	if after >= before {
		t.Fatalf("expected XP to decrease after decay, before=%d after=%d", before, after)
	}
}

func TestTierThresholds(t *testing.T) {
	tests := []struct {
		score float64
		tier  uint8
	}{
		{0.0, 0},
		{0.14, 0},
		{0.15, 1},
		{0.39, 1},
		{0.40, 2},
		{0.64, 2},
		{0.65, 3},
		{0.84, 3},
		{0.85, 4},
		{1.0, 4},
	}

	for _, tt := range tests {
		got := scoreTier(tt.score)
		if got != tt.tier {
			t.Errorf("scoreTier(%.2f) = %d, want %d", tt.score, got, tt.tier)
		}
	}
}

func TestAllReputations(t *testing.T) {
	m := NewManager(nil)
	addrs := []common.Address{
		common.HexToAddress("0x1"),
		common.HexToAddress("0x2"),
		common.HexToAddress("0x3"),
	}

	for _, addr := range addrs {
		m.Register(addr)
	}

	all := m.AllReputations()
	if len(all) != 3 {
		t.Fatalf("expected 3 reputations, got %d", len(all))
	}
}
