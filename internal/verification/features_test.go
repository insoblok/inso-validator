package verification

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	syncPkg "github.com/insoblok/inso-validator/internal/sync"
)

// ── Adaptive Block Verification Tests ───────────────────────────────────────

func TestVerifyAdaptiveBlock_Valid(t *testing.T) {
	eng := NewEngine()
	bounds := DefaultAdaptiveBounds()

	block := &syncPkg.L2Block{
		Number:   100,
		GasUsed:  20_000_000,
		GasLimit: 30_000_000,
		TxCount:  500,
	}

	result := eng.VerifyAdaptiveBlock(block, bounds)
	if !result.Valid {
		t.Fatalf("expected valid, got error: %s", result.Error)
	}
	if !result.GasLimitValid {
		t.Fatal("gas limit should be valid")
	}
	if !result.TxCountValid {
		t.Fatal("tx count should be valid")
	}
}

func TestVerifyAdaptiveBlock_GasLimitTooHigh(t *testing.T) {
	eng := NewEngine()
	bounds := DefaultAdaptiveBounds()

	block := &syncPkg.L2Block{
		Number:   101,
		GasUsed:  10_000_000,
		GasLimit: 100_000_000, // exceeds max 60M
		TxCount:  100,
	}

	result := eng.VerifyAdaptiveBlock(block, bounds)
	if result.Valid {
		t.Fatal("expected invalid for gas limit exceeding max")
	}
	if result.GasLimitValid {
		t.Fatal("gas limit should be invalid")
	}
}

func TestVerifyAdaptiveBlock_GasLimitTooLow(t *testing.T) {
	eng := NewEngine()
	bounds := DefaultAdaptiveBounds()

	block := &syncPkg.L2Block{
		Number:   102,
		GasUsed:  1_000_000,
		GasLimit: 5_000_000, // below min 15M
		TxCount:  10,
	}

	result := eng.VerifyAdaptiveBlock(block, bounds)
	if result.Valid {
		t.Fatal("expected invalid for gas limit below min")
	}
}

func TestVerifyAdaptiveBlock_TxCountExceedsMax(t *testing.T) {
	eng := NewEngine()
	bounds := DefaultAdaptiveBounds()

	block := &syncPkg.L2Block{
		Number:   103,
		GasUsed:  20_000_000,
		GasLimit: 30_000_000,
		TxCount:  15_000, // exceeds max 10K
	}

	result := eng.VerifyAdaptiveBlock(block, bounds)
	if result.Valid {
		t.Fatal("expected invalid for tx count exceeding max")
	}
	if result.TxCountValid {
		t.Fatal("tx count should be invalid")
	}
}

func TestVerifyAdaptiveBlock_GasUsedExceedsLimit(t *testing.T) {
	eng := NewEngine()
	bounds := DefaultAdaptiveBounds()

	block := &syncPkg.L2Block{
		Number:   104,
		GasUsed:  35_000_000, // exceeds gas limit
		GasLimit: 30_000_000,
		TxCount:  100,
	}

	result := eng.VerifyAdaptiveBlock(block, bounds)
	if result.Valid {
		t.Fatal("expected invalid when gas used exceeds gas limit")
	}
}

func TestVerifyAdaptiveBlock_ZeroGasLimit(t *testing.T) {
	eng := NewEngine()
	bounds := DefaultAdaptiveBounds()

	block := &syncPkg.L2Block{
		Number:  105,
		TxCount: 100,
	}

	// Zero gas limit should be valid (not reported)
	result := eng.VerifyAdaptiveBlock(block, bounds)
	if !result.Valid {
		t.Fatalf("expected valid with zero gas limit, got: %s", result.Error)
	}
}

func TestVerifyAdaptiveBlock_BoundaryValues(t *testing.T) {
	eng := NewEngine()
	bounds := DefaultAdaptiveBounds()

	tests := []struct {
		name     string
		gasLimit uint64
		txCount  int
		valid    bool
	}{
		{"min gas limit", 15_000_000, 100, true},
		{"max gas limit", 60_000_000, 100, true},
		{"max tx count", 30_000_000, 10_000, true},
		{"one below min gas", 14_999_999, 100, false},
		{"one above max gas", 60_000_001, 100, false},
		{"one above max tx", 30_000_000, 10_001, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			block := &syncPkg.L2Block{
				Number:   200,
				GasUsed:  10_000_000,
				GasLimit: tc.gasLimit,
				TxCount:  tc.txCount,
			}
			result := eng.VerifyAdaptiveBlock(block, bounds)
			if result.Valid != tc.valid {
				t.Errorf("expected valid=%v, got valid=%v (error: %s)", tc.valid, result.Valid, result.Error)
			}
		})
	}
}

// ── Lane Verification Tests ─────────────────────────────────────────────────

func TestVerifyLaneAllocation_Enabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": LaneStats{
				Fast:     50,
				Standard: 40,
				Slow:     10,
				Total:    100,
				Enabled:  true,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	eng := NewEngine()
	result := eng.VerifyLaneAllocation(server.URL)

	if !result.Valid {
		t.Fatalf("expected valid, got error: %s", result.Error)
	}
	if !result.LanesEnabled {
		t.Fatal("lanes should be enabled")
	}
	if result.FastCount != 50 || result.StandardCount != 40 || result.SlowCount != 10 {
		t.Fatalf("unexpected lane counts: fast=%d std=%d slow=%d",
			result.FastCount, result.StandardCount, result.SlowCount)
	}
}

func TestVerifyLaneAllocation_Disabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": LaneStats{
				Enabled: false,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	eng := NewEngine()
	result := eng.VerifyLaneAllocation(server.URL)

	if !result.Valid {
		t.Fatalf("expected valid when lanes disabled, got: %s", result.Error)
	}
	if result.LanesEnabled {
		t.Fatal("lanes should be disabled")
	}
}

func TestVerifyLaneAllocation_CountMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": LaneStats{
				Fast:     50,
				Standard: 40,
				Slow:     10,
				Total:    200, // doesn't match sum
				Enabled:  true,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	eng := NewEngine()
	result := eng.VerifyLaneAllocation(server.URL)

	if result.Valid {
		t.Fatal("expected invalid when lane counts don't sum to total")
	}
}

func TestVerifyLaneAllocation_RPCError(t *testing.T) {
	eng := NewEngine()
	// Use an invalid URL to trigger an error
	result := eng.VerifyLaneAllocation("http://127.0.0.1:1")

	if result.Valid {
		t.Fatal("expected invalid on RPC error")
	}
	if result.Error == "" {
		t.Fatal("expected error message")
	}
}

// ── Compute Receipt Verification Tests ──────────────────────────────────────

func makeTestReceipt() *ComputeReceiptData {
	r := &ComputeReceiptData{
		TxHash:        "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		BlockNumber:   42,
		TxIndex:       0,
		Sender:        "0x1234567890abcdef1234567890abcdef12345678",
		GasUsed:       21000,
		Status:        1,
		PreStateRoot:  "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PostStateRoot: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		InputHash:     "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		OutputHash:    "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		LogsHash:      "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Verified:      true,
	}

	// Compute the correct hash
	hash := computeReceiptHash(r)
	r.ReceiptHash = hash.Hex()

	return r
}

func TestVerifyComputeReceipt_Valid(t *testing.T) {
	receipt := makeTestReceipt()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  receipt,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	eng := NewEngine()
	result := eng.VerifyComputeReceipt(server.URL, receipt.TxHash)

	if !result.Valid {
		t.Fatalf("expected valid receipt, got error: %s", result.Error)
	}
	if !result.Verified {
		t.Fatal("receipt should be verified")
	}
}

func TestVerifyComputeReceipt_HashMismatch(t *testing.T) {
	receipt := makeTestReceipt()
	receipt.ReceiptHash = "0xdeadbeef" // wrong hash

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  receipt,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	eng := NewEngine()
	result := eng.VerifyComputeReceipt(server.URL, receipt.TxHash)

	if result.Valid {
		t.Fatal("expected invalid for hash mismatch")
	}
}

func TestVerifyComputeReceipt_Unverified(t *testing.T) {
	receipt := makeTestReceipt()
	receipt.Verified = false
	// Recompute hash since verified is not part of the hash
	hash := computeReceiptHash(receipt)
	receipt.ReceiptHash = hash.Hex()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  receipt,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	eng := NewEngine()
	result := eng.VerifyComputeReceipt(server.URL, receipt.TxHash)

	if result.Valid {
		t.Fatal("expected invalid for unverified receipt")
	}
}

func TestVerifyComputeReceipt_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  nil,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	eng := NewEngine()
	result := eng.VerifyComputeReceipt(server.URL, "0xdeadbeef")

	if result.Valid {
		t.Fatal("expected invalid for missing receipt")
	}
	if result.Error != "receipt not found" {
		t.Fatalf("expected 'receipt not found' error, got: %s", result.Error)
	}
}

// ── computeReceiptHash Unit Tests ───────────────────────────────────────────

func TestComputeReceiptHash_Deterministic(t *testing.T) {
	r := makeTestReceipt()
	hash1 := computeReceiptHash(r)
	hash2 := computeReceiptHash(r)

	if hash1 != hash2 {
		t.Fatal("receipt hash should be deterministic")
	}
}

func TestComputeReceiptHash_DifferentInputs(t *testing.T) {
	r1 := makeTestReceipt()
	r2 := makeTestReceipt()
	r2.GasUsed = 42000

	hash1 := computeReceiptHash(r1)
	hash2 := computeReceiptHash(r2)

	if hash1 == hash2 {
		t.Fatal("different inputs should produce different hashes")
	}
}

// ── Existing engine test ────────────────────────────────────────────────────

func TestVerifyBlock_Valid(t *testing.T) {
	eng := NewEngine()

	// Compute the expected state root the same way the engine does
	block := &syncPkg.L2Block{
		Number:    1,
		Timestamp: 1000,
		GasUsed:   21000,
	}

	// Engine uses: H(blockNum || currentStateRoot || timestamp || gasUsed)
	h := sha256.New()
	h.Write(new(big.Int).SetUint64(1).Bytes())
	h.Write(eng.stateRoot.Bytes())
	h.Write(new(big.Int).SetUint64(1000).Bytes())
	h.Write(new(big.Int).SetUint64(21000).Bytes())
	expectedRoot := common.BytesToHash(h.Sum(nil))

	block.StateRoot = expectedRoot.Hex()

	result := eng.VerifyBlock(block)
	if !result.Valid {
		t.Fatalf("expected valid, got error: %s", result.Error)
	}
}

func TestVerifyBlock_InvalidBlockNumber(t *testing.T) {
	eng := NewEngine()

	block := &syncPkg.L2Block{
		Number: 0,
	}

	result := eng.VerifyBlock(block)
	if result.Valid {
		t.Fatal("block number 0 should be invalid")
	}
}

func TestVerifyBlock_StateRootMismatch(t *testing.T) {
	eng := NewEngine()

	block := &syncPkg.L2Block{
		Number:    1,
		Timestamp: 1000,
		GasUsed:   21000,
		StateRoot: "0xdeadbeef",
	}

	result := eng.VerifyBlock(block)
	if result.Valid {
		t.Fatal("mismatched state root should be invalid")
	}
}

// ── Helper tests ────────────────────────────────────────────────────────────

func TestUint64ToBytes(t *testing.T) {
	tests := []struct {
		input    uint64
		expected int
	}{
		{0, 8},
		{1, 8},
		{255, 8},
		{1 << 32, 8},
	}

	for _, tc := range tests {
		b := uint64ToBytes(tc.input)
		if len(b) != tc.expected {
			t.Errorf("uint64ToBytes(%d): expected len %d, got %d", tc.input, tc.expected, len(b))
		}
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello world", 5) != "hello" {
		t.Fatal("truncate should cut string")
	}
	if truncate("hi", 5) != "hi" {
		t.Fatal("truncate should preserve short strings")
	}
}

func TestDefaultAdaptiveBounds(t *testing.T) {
	bounds := DefaultAdaptiveBounds()
	if bounds.MinGasLimit != 15_000_000 {
		t.Fatalf("expected min gas 15M, got %d", bounds.MinGasLimit)
	}
	if bounds.MaxGasLimit != 60_000_000 {
		t.Fatalf("expected max gas 60M, got %d", bounds.MaxGasLimit)
	}
	if bounds.MinMaxTx != 1_000 {
		t.Fatalf("expected min tx 1000, got %d", bounds.MinMaxTx)
	}
	if bounds.MaxMaxTx != 10_000 {
		t.Fatalf("expected max tx 10000, got %d", bounds.MaxMaxTx)
	}
}

// Suppress unused import warning
var _ = fmt.Sprintf
