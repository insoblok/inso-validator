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
	"github.com/insoblok/inso-validator/internal/p2p"
)

// L2Block is a simplified block structure received from the sequencer.
type L2Block struct {
	Number     uint64 `json:"number"`
	Hash       string `json:"hash"`
	ParentHash string `json:"parentHash"`
	Timestamp  uint64 `json:"timestamp"`
	StateRoot  string `json:"stateRoot"`
	GasUsed    uint64 `json:"gasUsed"`
	GasLimit   uint64 `json:"gasLimit"`
	TxCount    int    `json:"txCount"`
	Sequencer  string `json:"sequencer"`
}

// Engine syncs L2 blocks from the sequencer via P2P (primary) + RPC (fallback).
type Engine struct {
	mu            sync.RWMutex
	cfg           *config.SequencerConfig
	network       *p2p.Network
	httpClient    *http.Client
	latestBlock   uint64
	blocks        map[uint64]*L2Block
	logger        log.Logger
	cancel        context.CancelFunc
	onNewBlock    func(*L2Block) // callback when a new block is received
}

// NewEngine creates a new sync engine.
func NewEngine(cfg *config.SequencerConfig, network *p2p.Network) *Engine {
	return &Engine{
		cfg:     cfg,
		network: network,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		blocks: make(map[uint64]*L2Block, 4096),
		logger: log.New("module", "sync"),
	}
}

// OnNewBlock registers a callback for each new block.
func (e *Engine) OnNewBlock(fn func(*L2Block)) {
	e.onNewBlock = fn
}

// Start begins the sync engine: P2P subscription + RPC polling fallback.
func (e *Engine) Start(ctx context.Context) {
	ctx, e.cancel = context.WithCancel(ctx)

	e.logger.Info("Sync engine started",
		"sequencerRPC", e.cfg.RPCURL,
		"mode", "p2p+rpc",
	)

	// P2P block listener (primary path)
	go e.p2pBlockListener(ctx)

	// RPC polling fallback (catch-up and gap-fill)
	go e.rpcSyncLoop(ctx)
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

// ── P2P block listener ───────────────────────────────────────────────────────

// p2pBlockListener receives new blocks from the P2P gossip network.
func (e *Engine) p2pBlockListener(ctx context.Context) {
	if e.network == nil {
		e.logger.Debug("No P2P network configured, skipping P2P sync")
		return
	}

	msgCh := e.network.Messages()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			if msg.Type == p2p.MsgNewBlock {
				e.handleP2PBlock(msg)
			}
		}
	}
}

func (e *Engine) handleP2PBlock(msg *p2p.Message) {
	announce, err := p2p.DecodeBlockAnnounce(msg.Payload)
	if err != nil {
		e.logger.Debug("Invalid block announce from P2P", "err", err, "from", msg.From)
		return
	}

	block := &L2Block{
		Number:     announce.Number,
		Hash:       announce.Hash,
		ParentHash: announce.ParentHash,
		Timestamp:  announce.Timestamp,
		StateRoot:  announce.StateRoot,
		GasUsed:    announce.GasUsed,
		GasLimit:   announce.GasLimit,
		TxCount:    announce.TxCount,
		Sequencer:  announce.Sequencer,
	}

	e.processBlock(block, "p2p")
}

// ── RPC polling fallback ─────────────────────────────────────────────────────

func (e *Engine) rpcSyncLoop(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("RPC sync loop stopped")
			return
		case <-ticker.C:
			e.syncFromRPC(ctx)
		}
	}
}

func (e *Engine) syncFromRPC(ctx context.Context) {
	remoteLatest, err := e.fetchLatestBlockNumber()
	if err != nil {
		e.logger.Debug("Failed to fetch latest block from RPC", "err", err)
		return
	}

	localLatest := e.LatestBlock()
	if remoteLatest <= localLatest {
		return
	}

	// Catch up sequentially (could be parallelized for large gaps)
	for num := localLatest + 1; num <= remoteLatest; num++ {
		block, err := e.fetchBlock(ctx, num)
		if err != nil {
			e.logger.Warn("Failed to fetch block from RPC", "number", num, "err", err)
			return
		}
		e.processBlock(block, "rpc")
	}
}

// ── Common processing ────────────────────────────────────────────────────────

func (e *Engine) processBlock(block *L2Block, source string) {
	e.mu.Lock()

	// Skip if already synced
	if _, exists := e.blocks[block.Number]; exists {
		e.mu.Unlock()
		return
	}

	// Basic validation: block number must be next in sequence (or first block)
	if block.Number > 1 && block.Number != e.latestBlock+1 {
		// Gap detected — let RPC fill it in later
		e.mu.Unlock()
		e.logger.Debug("Block gap detected, deferring",
			"expected", e.latestBlock+1,
			"got", block.Number,
			"source", source,
		)
		return
	}

	e.blocks[block.Number] = block
	e.latestBlock = block.Number
	e.mu.Unlock()

	e.logger.Debug("Block synced",
		"number", block.Number,
		"txCount", block.TxCount,
		"source", source,
	)

	if e.onNewBlock != nil {
		e.onNewBlock(block)
	}
}

// ── RPC helpers ──────────────────────────────────────────────────────────────

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
