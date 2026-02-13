package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-validator/internal/config"
)

// L2Block is a simplified block structure received from the sequencer.
type L2Block struct {
	Number     uint64 `json:"number"`
	Hash       string `json:"hash"`
	ParentHash string `json:"parentHash"`
	Timestamp  uint64 `json:"timestamp"`
	StateRoot  string `json:"stateRoot"`
	GasUsed    uint64 `json:"gasUsed"`
	TxCount    int    `json:"txCount"`
}

// Engine syncs L2 blocks from the sequencer RPC.
type Engine struct {
	mu            sync.RWMutex
	cfg           *config.SequencerConfig
	httpClient    *http.Client
	latestBlock   uint64
	blocks        map[uint64]*L2Block
	logger        log.Logger
	cancel        context.CancelFunc
	onNewBlock    func(*L2Block) // callback when a new block is received
}

// NewEngine creates a new sync engine.
func NewEngine(cfg *config.SequencerConfig) *Engine {
	return &Engine{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		blocks: make(map[uint64]*L2Block, 1024),
		logger: log.New("module", "sync"),
	}
}

// OnNewBlock registers a callback for each new block.
func (e *Engine) OnNewBlock(fn func(*L2Block)) {
	e.onNewBlock = fn
}

// Start begins polling the sequencer for new blocks.
func (e *Engine) Start(ctx context.Context) {
	ctx, e.cancel = context.WithCancel(ctx)

	e.logger.Info("Sync engine started",
		"sequencerRPC", e.cfg.RPCURL,
	)

	go e.syncLoop(ctx)
}

// Stop halts the sync engine.
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
}

// LatestBlock returns the most recently synced block number.
func (e *Engine) LatestBlock() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.latestBlock
}

// GetBlock returns a synced block by number.
func (e *Engine) GetBlock(num uint64) *L2Block {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.blocks[num]
}

// IsSynced returns true if we're caught up to the sequencer.
func (e *Engine) IsSynced() bool {
	latest, err := e.fetchLatestBlockNumber()
	if err != nil {
		return false
	}
	return e.LatestBlock() >= latest
}

func (e *Engine) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("Sync engine stopped")
			return
		case <-ticker.C:
			e.syncNew(ctx)
		}
	}
}

func (e *Engine) syncNew(ctx context.Context) {
	remoteLatest, err := e.fetchLatestBlockNumber()
	if err != nil {
		e.logger.Debug("Failed to fetch latest block", "err", err)
		return
	}

	localLatest := e.LatestBlock()
	if remoteLatest <= localLatest {
		return
	}

	// Sync blocks sequentially (could be parallelized for catch-up)
	for num := localLatest + 1; num <= remoteLatest; num++ {
		block, err := e.fetchBlock(ctx, num)
		if err != nil {
			e.logger.Warn("Failed to fetch block", "number", num, "err", err)
			return
		}

		e.mu.Lock()
		e.blocks[num] = block
		e.latestBlock = num
		e.mu.Unlock()

		if e.onNewBlock != nil {
			e.onNewBlock(block)
		}

		e.logger.Debug("Block synced", "number", num, "txCount", block.TxCount)
	}
}

func (e *Engine) fetchLatestBlockNumber() (uint64, error) {
	result, err := e.rpcCall("eth_blockNumber", nil)
	if err != nil {
		return 0, err
	}

	var hexNum string
	if err := json.Unmarshal(result, &hexNum); err != nil {
		return 0, fmt.Errorf("decode block number: %w", err)
	}

	var num uint64
	fmt.Sscanf(hexNum, "0x%x", &num)
	return num, nil
}

func (e *Engine) fetchBlock(ctx context.Context, num uint64) (*L2Block, error) {
	hexNum := fmt.Sprintf("0x%x", num)
	result, err := e.rpcCall("eth_getBlockByNumber", []interface{}{hexNum, false})
	if err != nil {
		return nil, err
	}

	var block L2Block
	if err := json.Unmarshal(result, &block); err != nil {
		return nil, fmt.Errorf("decode block: %w", err)
	}

	return &block, nil
}

func (e *Engine) rpcCall(method string, params interface{}) (json.RawMessage, error) {
	if params == nil {
		params = []interface{}{}
	}

	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      1,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	resp, err := e.httpClient.Post(e.cfg.RPCURL, "application/json", io.NopCloser(
		// Use bytes.NewReader instead
		jsonReader(body),
	))
	if err != nil {
		return nil, fmt.Errorf("rpc call: %w", err)
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

type bytesReader struct {
	data []byte
	pos  int
}

func jsonReader(data []byte) io.Reader {
	return &bytesReader{data: data}
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
