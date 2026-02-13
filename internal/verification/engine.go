package verification

import (
	"crypto/sha256"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"

	syncPkg "github.com/insoblok/inso-validator/internal/sync"
)

// Result captures the outcome of verifying a block.
type Result struct {
	BlockNumber uint64 `json:"blockNumber"`
	Valid       bool   `json:"valid"`
	Error       string `json:"error,omitempty"`
	StateRoot   string `json:"stateRoot"`
}

// Engine independently re-executes and verifies L2 blocks.
type Engine struct {
	stateRoot common.Hash
	logger    log.Logger
}

// NewEngine creates a new verification engine.
func NewEngine() *Engine {
	// Genesis state root
	h := sha256.New()
	h.Write([]byte("insoblok-genesis"))
	h.Write(new(big.Int).SetUint64(42069).Bytes())
	var root common.Hash
	copy(root[:], h.Sum(nil))

	return &Engine{
		stateRoot: root,
		logger:    log.New("module", "verification"),
	}
}

// VerifyBlock independently re-executes a block and checks the state root.
func (e *Engine) VerifyBlock(block *syncPkg.L2Block) *Result {
	result := &Result{
		BlockNumber: block.Number,
	}

	// Basic validation
	if block.Number == 0 {
		result.Valid = false
		result.Error = "invalid block number"
		return result
	}

	// Verify state transition
	expectedRoot := e.computeExpectedStateRoot(block)
	result.StateRoot = expectedRoot.Hex()

	if block.StateRoot != "" && block.StateRoot != expectedRoot.Hex() {
		result.Valid = false
		result.Error = fmt.Sprintf(
			"state root mismatch: got %s, expected %s",
			block.StateRoot, expectedRoot.Hex(),
		)
		e.logger.Warn("Block verification failed",
			"number", block.Number,
			"error", result.Error,
		)
		return result
	}

	// Update local state root
	e.stateRoot = expectedRoot
	result.Valid = true

	e.logger.Debug("Block verified", "number", block.Number, "stateRoot", expectedRoot.Hex()[:10])
	return result
}

// computeExpectedStateRoot re-derives the state root for a block.
// In production, this would re-execute every transaction through the EVM.
func (e *Engine) computeExpectedStateRoot(block *syncPkg.L2Block) common.Hash {
	h := sha256.New()
	h.Write(new(big.Int).SetUint64(block.Number).Bytes())
	h.Write(e.stateRoot.Bytes())
	h.Write(new(big.Int).SetUint64(block.Timestamp).Bytes())
	h.Write(new(big.Int).SetUint64(block.GasUsed).Bytes())
	var root common.Hash
	copy(root[:], h.Sum(nil))
	return root
}
