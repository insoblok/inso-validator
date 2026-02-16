package sync

import (
	"testing"

	"github.com/insoblok/inso-validator/internal/config"
)

// newTestEngine creates a sync engine with no P2P and a dummy RPC URL.
func newTestEngine() *Engine {
	cfg := &config.SequencerConfig{
		RPCURL: "http://localhost:9999", // unreachable, for unit tests only
	}
	return NewEngine(cfg, nil)
}

// ── NewEngine ────────────────────────────────────────────────────────────────

func TestNewEngine(t *testing.T) {
	eng := newTestEngine()
	if eng == nil {
		t.Fatal("NewEngine returned nil")
	}
	if eng.latestBlock != 0 {
		t.Errorf("expected latestBlock=0, got %d", eng.latestBlock)
	}
	if eng.blocks == nil {
		t.Fatal("blocks map is nil")
	}
	if eng.httpClient == nil {
		t.Fatal("httpClient is nil")
	}
}

// ── LatestBlock / GetBlock ───────────────────────────────────────────────────

func TestLatestBlockInitiallyZero(t *testing.T) {
	eng := newTestEngine()
	if eng.LatestBlock() != 0 {
		t.Errorf("expected 0, got %d", eng.LatestBlock())
	}
}

func TestGetBlockNotFound(t *testing.T) {
	eng := newTestEngine()
	if eng.GetBlock(1) != nil {
		t.Error("expected nil for non-existent block")
	}
}

// ── processBlock ─────────────────────────────────────────────────────────────

func TestProcessBlockFirst(t *testing.T) {
	eng := newTestEngine()

	block := &L2Block{
		Number:    1,
		Hash:      "0xabc",
		Timestamp: 1000,
		GasUsed:   21000,
		TxCount:   1,
	}

	eng.processBlock(block, "test")

	if eng.LatestBlock() != 1 {
		t.Errorf("expected latestBlock=1, got %d", eng.LatestBlock())
	}
	if eng.GetBlock(1) == nil {
		t.Error("expected block 1 to be stored")
	}
}

func TestProcessBlockSequential(t *testing.T) {
	eng := newTestEngine()

	for i := uint64(1); i <= 5; i++ {
		eng.processBlock(&L2Block{Number: i, Hash: "0x" + string(rune('a'+i))}, "test")
	}

	if eng.LatestBlock() != 5 {
		t.Errorf("expected latestBlock=5, got %d", eng.LatestBlock())
	}
	for i := uint64(1); i <= 5; i++ {
		if eng.GetBlock(i) == nil {
			t.Errorf("expected block %d to be stored", i)
		}
	}
}

func TestProcessBlockDuplicate(t *testing.T) {
	eng := newTestEngine()

	block := &L2Block{Number: 1, Hash: "0xfirst"}
	eng.processBlock(block, "test")

	// Process same block again — should be ignored
	engDup := &L2Block{Number: 1, Hash: "0xduplicate"}
	eng.processBlock(engDup, "test")

	if eng.GetBlock(1).Hash != "0xfirst" {
		t.Errorf("expected original hash, got %s", eng.GetBlock(1).Hash)
	}
}

func TestProcessBlockGapRejected(t *testing.T) {
	eng := newTestEngine()

	eng.processBlock(&L2Block{Number: 1, Hash: "0xa"}, "test")

	// Block 3 without block 2 — should be deferred (gap)
	eng.processBlock(&L2Block{Number: 3, Hash: "0xc"}, "test")

	if eng.LatestBlock() != 1 {
		t.Errorf("expected latestBlock=1 (gap deferred), got %d", eng.LatestBlock())
	}
	if eng.GetBlock(3) != nil {
		t.Error("block 3 should not have been stored (gap)")
	}
}

func TestProcessBlockGapFill(t *testing.T) {
	eng := newTestEngine()

	eng.processBlock(&L2Block{Number: 1, Hash: "0xa"}, "test")
	// Gap: block 3 arrives first
	eng.processBlock(&L2Block{Number: 3, Hash: "0xc"}, "test")
	// Fill: block 2 arrives
	eng.processBlock(&L2Block{Number: 2, Hash: "0xb"}, "test")
	// Now block 3 should work
	eng.processBlock(&L2Block{Number: 3, Hash: "0xc"}, "test")

	if eng.LatestBlock() != 3 {
		t.Errorf("expected latestBlock=3, got %d", eng.LatestBlock())
	}
}

// ── OnNewBlock callback ──────────────────────────────────────────────────────

func TestOnNewBlockCallback(t *testing.T) {
	eng := newTestEngine()

	var callbackBlocks []uint64
	eng.OnNewBlock(func(b *L2Block) {
		callbackBlocks = append(callbackBlocks, b.Number)
	})

	eng.processBlock(&L2Block{Number: 1, Hash: "0xa"}, "test")
	eng.processBlock(&L2Block{Number: 2, Hash: "0xb"}, "test")

	if len(callbackBlocks) != 2 {
		t.Fatalf("expected 2 callbacks, got %d", len(callbackBlocks))
	}
	if callbackBlocks[0] != 1 || callbackBlocks[1] != 2 {
		t.Errorf("expected [1,2], got %v", callbackBlocks)
	}
}

func TestOnNewBlockNotCalledForDuplicate(t *testing.T) {
	eng := newTestEngine()

	calls := 0
	eng.OnNewBlock(func(b *L2Block) {
		calls++
	})

	eng.processBlock(&L2Block{Number: 1, Hash: "0xa"}, "test")
	eng.processBlock(&L2Block{Number: 1, Hash: "0xa"}, "test") // duplicate

	if calls != 1 {
		t.Errorf("expected 1 callback (duplicate ignored), got %d", calls)
	}
}

// ── L2Block fields ───────────────────────────────────────────────────────────

func TestL2BlockFieldsPreserved(t *testing.T) {
	eng := newTestEngine()

	block := &L2Block{
		Number:     1,
		Hash:       "0xdeadbeef",
		ParentHash: "0x0000",
		Timestamp:  1700000000,
		StateRoot:  "0xstateroot",
		GasUsed:    50000,
		GasLimit:   30000000,
		TxCount:    42,
		Sequencer:  "0xseq",
	}

	eng.processBlock(block, "test")

	got := eng.GetBlock(1)
	if got.Hash != "0xdeadbeef" {
		t.Errorf("hash mismatch: %s", got.Hash)
	}
	if got.ParentHash != "0x0000" {
		t.Errorf("parentHash mismatch: %s", got.ParentHash)
	}
	if got.TxCount != 42 {
		t.Errorf("txCount mismatch: %d", got.TxCount)
	}
	if got.GasLimit != 30000000 {
		t.Errorf("gasLimit mismatch: %d", got.GasLimit)
	}
	if got.Sequencer != "0xseq" {
		t.Errorf("sequencer mismatch: %s", got.Sequencer)
	}
}

// ── Stop safety ──────────────────────────────────────────────────────────────

func TestStopBeforeStart(t *testing.T) {
	eng := newTestEngine()
	// Stop without Start should not panic
	eng.Stop()
}
