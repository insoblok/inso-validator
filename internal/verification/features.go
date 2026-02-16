package verification

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"

	syncPkg "github.com/insoblok/inso-validator/internal/sync"
)

// ── Lane Verification (Feature #1) ──────────────────────────────────────────

// LaneStats represents per-lane transaction statistics from the sequencer.
type LaneStats struct {
	Fast     int  `json:"fast"`
	Standard int  `json:"standard"`
	Slow     int  `json:"slow"`
	Total    int  `json:"total"`
	Enabled  bool `json:"enabled"`
}

// LaneVerificationResult captures the outcome of lane allocation checks.
type LaneVerificationResult struct {
	BlockNumber   uint64 `json:"blockNumber"`
	LanesEnabled  bool   `json:"lanesEnabled"`
	FastCount     int    `json:"fastCount"`
	StandardCount int    `json:"standardCount"`
	SlowCount     int    `json:"slowCount"`
	Valid         bool   `json:"valid"`
	Error         string `json:"error,omitempty"`
}

// VerifyLaneAllocation fetches live lane stats from the sequencer and validates
// that lanes are operational. Gas allocation ratios are enforced by the
// sequencer's LanedMempool (Fast 40%, Standard 40%, Slow 20%); the validator
// checks that the sequencer is correctly reporting lane data.
func (e *Engine) VerifyLaneAllocation(sequencerURL string) *LaneVerificationResult {
	result := &LaneVerificationResult{}

	stats, err := fetchLaneStats(sequencerURL)
	if err != nil {
		result.Error = fmt.Sprintf("failed to fetch lane stats: %v", err)
		return result
	}

	result.LanesEnabled = stats.Enabled
	result.FastCount = stats.Fast
	result.StandardCount = stats.Standard
	result.SlowCount = stats.Slow

	if !stats.Enabled {
		result.Valid = true // lanes not enabled, nothing to verify
		return result
	}

	// Validate internal consistency: total == sum of lanes
	expectedTotal := stats.Fast + stats.Standard + stats.Slow
	if stats.Total > 0 && stats.Total != expectedTotal {
		result.Valid = false
		result.Error = fmt.Sprintf(
			"lane count mismatch: total=%d but fast+std+slow=%d",
			stats.Total, expectedTotal,
		)
		return result
	}

	result.Valid = true
	e.logger.Debug("Lane allocation verified",
		"fast", stats.Fast, "standard", stats.Standard, "slow", stats.Slow,
	)
	return result
}

func fetchLaneStats(sequencerURL string) (*LaneStats, error) {
	body := `{"jsonrpc":"2.0","id":1,"method":"inso_getLaneStats","params":[]}`

	resp, err := rpcPost(sequencerURL, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rpcResp struct {
		Result LaneStats `json:"result"`
	}
	if err := json.Unmarshal(data, &rpcResp); err != nil {
		return nil, err
	}
	return &rpcResp.Result, nil
}

// ── Compute Receipt Verification (Feature #10) ─────────────────────────────

// ComputeReceiptData represents a verifiable compute receipt from the sequencer.
type ComputeReceiptData struct {
	TxHash        string `json:"txHash"`
	BlockNumber   uint64 `json:"blockNumber"`
	TxIndex       int    `json:"txIndex"`
	Sender        string `json:"sender"`
	GasUsed       uint64 `json:"gasUsed"`
	Status        uint64 `json:"status"`
	PreStateRoot  string `json:"preStateRoot"`
	PostStateRoot string `json:"postStateRoot"`
	InputHash     string `json:"inputHash"`
	OutputHash    string `json:"outputHash"`
	LogsHash      string `json:"logsHash"`
	ReceiptHash   string `json:"receiptHash"`
	Verified      bool   `json:"verified"`
}

// ReceiptVerificationResult captures the outcome of receipt verification.
type ReceiptVerificationResult struct {
	TxHash      string `json:"txHash"`
	BlockNumber uint64 `json:"blockNumber"`
	Valid       bool   `json:"valid"`
	Error       string `json:"error,omitempty"`
	ReceiptHash string `json:"receiptHash"`
	Verified    bool   `json:"verified"`
}

// VerifyComputeReceipt fetches a compute receipt from the sequencer and
// independently recomputes the receipt hash to ensure data integrity.
func (e *Engine) VerifyComputeReceipt(sequencerURL string, txHash string) *ReceiptVerificationResult {
	result := &ReceiptVerificationResult{TxHash: txHash}

	receipt, err := fetchComputeReceipt(sequencerURL, txHash)
	if err != nil {
		result.Error = fmt.Sprintf("failed to fetch receipt: %v", err)
		return result
	}
	if receipt == nil {
		result.Error = "receipt not found"
		return result
	}

	result.BlockNumber = receipt.BlockNumber
	result.ReceiptHash = receipt.ReceiptHash

	// Recompute the receipt hash from constituent fields
	recomputed := computeReceiptHash(receipt)
	if recomputed.Hex() != receipt.ReceiptHash {
		result.Error = fmt.Sprintf(
			"receipt hash mismatch: got %s, recomputed %s",
			receipt.ReceiptHash, recomputed.Hex(),
		)
		result.Valid = false
		e.logger.Warn("Compute receipt verification failed", "txHash", txHash, "error", result.Error)
		return result
	}

	result.Verified = receipt.Verified
	if !receipt.Verified {
		result.Error = "sequencer reports receipt as unverified"
		result.Valid = false
		return result
	}

	result.Valid = true
	e.logger.Debug("Compute receipt verified", "txHash", truncate(txHash, 16), "block", receipt.BlockNumber)
	return result
}

// computeReceiptHash mirrors the sequencer's ComputeReceipt.computeHash().
func computeReceiptHash(r *ComputeReceiptData) common.Hash {
	h := sha256.New()
	h.Write(common.HexToHash(r.TxHash).Bytes())
	h.Write(uint64ToBytes(r.BlockNumber))
	h.Write(uint64ToBytes(uint64(r.TxIndex)))
	h.Write(common.HexToAddress(r.Sender).Bytes())
	h.Write(uint64ToBytes(r.GasUsed))
	h.Write(common.HexToHash(r.PreStateRoot).Bytes())
	h.Write(common.HexToHash(r.PostStateRoot).Bytes())
	h.Write(uint64ToBytes(r.Status))
	h.Write(common.HexToHash(r.InputHash).Bytes())
	h.Write(common.HexToHash(r.OutputHash).Bytes())
	h.Write(common.HexToHash(r.LogsHash).Bytes())
	return common.BytesToHash(h.Sum(nil))
}

func fetchComputeReceipt(sequencerURL string, txHash string) (*ComputeReceiptData, error) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"inso_getComputeReceipt","params":["%s"]}`, txHash)
	resp, err := rpcPost(sequencerURL, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rpcResp struct {
		Result *ComputeReceiptData `json:"result"`
	}
	if err := json.Unmarshal(data, &rpcResp); err != nil {
		return nil, err
	}
	return rpcResp.Result, nil
}

// ── Adaptive Block Verification (Feature #4) ────────────────────────────────

// AdaptiveBlockBounds defines the valid range for adaptive block parameters.
type AdaptiveBlockBounds struct {
	MinGasLimit uint64 `json:"minGasLimit"`
	MaxGasLimit uint64 `json:"maxGasLimit"`
	MinMaxTx    int    `json:"minMaxTx"`
	MaxMaxTx    int    `json:"maxMaxTx"`
}

// DefaultAdaptiveBounds returns the default adaptive block bounds.
func DefaultAdaptiveBounds() AdaptiveBlockBounds {
	return AdaptiveBlockBounds{
		MinGasLimit: 15_000_000,
		MaxGasLimit: 60_000_000,
		MinMaxTx:    1_000,
		MaxMaxTx:    10_000,
	}
}

// AdaptiveVerificationResult captures the outcome of adaptive block checks.
type AdaptiveVerificationResult struct {
	BlockNumber   uint64 `json:"blockNumber"`
	GasUsed       uint64 `json:"gasUsed"`
	GasLimit      uint64 `json:"gasLimit"`
	GasLimitValid bool   `json:"gasLimitValid"`
	TxCountValid  bool   `json:"txCountValid"`
	Valid         bool   `json:"valid"`
	Error         string `json:"error,omitempty"`
}

// VerifyAdaptiveBlock checks that a block's gas limit and tx count fall
// within the adaptive block sizing bounds.
func (e *Engine) VerifyAdaptiveBlock(block *syncPkg.L2Block, bounds AdaptiveBlockBounds) *AdaptiveVerificationResult {
	result := &AdaptiveVerificationResult{
		BlockNumber: block.Number,
		GasUsed:     block.GasUsed,
		GasLimit:    block.GasLimit,
	}

	// Check gas limit within adaptive bounds
	if block.GasLimit > 0 {
		result.GasLimitValid = block.GasLimit >= bounds.MinGasLimit && block.GasLimit <= bounds.MaxGasLimit
		if !result.GasLimitValid {
			result.Error = fmt.Sprintf(
				"gas limit %d outside bounds [%d, %d]",
				block.GasLimit, bounds.MinGasLimit, bounds.MaxGasLimit,
			)
		}
	} else {
		result.GasLimitValid = true // gas limit not reported
	}

	// Check transaction count within bounds
	if block.TxCount > 0 {
		result.TxCountValid = block.TxCount <= bounds.MaxMaxTx
		if !result.TxCountValid {
			result.Error = fmt.Sprintf("tx count %d exceeds max %d", block.TxCount, bounds.MaxMaxTx)
		}
	} else {
		result.TxCountValid = true
	}

	// Check gas used doesn't exceed gas limit
	if block.GasLimit > 0 && block.GasUsed > block.GasLimit {
		result.GasLimitValid = false
		result.Error = fmt.Sprintf("gas used %d exceeds gas limit %d", block.GasUsed, block.GasLimit)
	}

	result.Valid = result.GasLimitValid && result.TxCountValid

	if result.Valid {
		e.logger.Debug("Adaptive block verified",
			"number", block.Number, "gasLimit", block.GasLimit, "txCount", block.TxCount,
		)
	} else {
		e.logger.Warn("Adaptive block verification failed",
			"number", block.Number, "error", result.Error,
		)
	}

	return result
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func uint64ToBytes(v uint64) []byte {
	b := new(big.Int).SetUint64(v).Bytes()
	padded := make([]byte, 8)
	copy(padded[8-len(b):], b)
	return padded
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func rpcPost(url, body string) (*http.Response, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	return client.Post(url, "application/json", bytes.NewBufferString(body))
}

// Ensure logger interface is available on Engine
// (already defined in engine.go, this file extends Engine with new methods)
var _ = log.New // suppress unused import
