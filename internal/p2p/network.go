package p2p

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-validator/internal/config"
)

// PeerID is a unique identifier for a peer on the network.
type PeerID string

// Peer represents a connected peer.
type Peer struct {
	ID        PeerID   `json:"id"`
	Address   string   `json:"address"`
	Version   string   `json:"version"`
	IsActive  bool     `json:"isActive"`
	Latency   int64    `json:"latencyMs"`
	conn      net.Conn // underlying TCP conn
}

// Message represents a P2P protocol message.
type Message struct {
	Type    MessageType `json:"type"`
	From    PeerID      `json:"from"`
	Payload []byte      `json:"payload"`
}

// MessageType identifies the P2P message kind.
type MessageType int

const (
	MsgNewBlock MessageType = iota
	MsgAttestation
	MsgSyncRequest
	MsgSyncResponse
	MsgValidatorAnnounce
	MsgSlashReport
)

// wireMessage is the on-the-wire JSON-encoded envelope.
type wireMessage struct {
	Type    int    `json:"t"`
	From    string `json:"f"`
	Payload []byte `json:"p"`
}

// Network manages the TCP-based P2P overlay network for validators.
type Network struct {
	mu        sync.RWMutex
	cfg       *config.ValidatorConfig
	peers     map[PeerID]*Peer
	msgCh     chan *Message
	logger    log.Logger
	cancel    context.CancelFunc
	listener  net.Listener
	localID   PeerID
	bootnodes []string
}

// NewNetwork creates a new P2P network manager.
func NewNetwork(cfg *config.ValidatorConfig) *Network {
	// Derive a local peer ID from listen address + port
	localID := PeerID(fmt.Sprintf("validator-%d", cfg.P2PPort))
	return &Network{
		cfg:       cfg,
		peers:     make(map[PeerID]*Peer),
		msgCh:     make(chan *Message, 4096),
		logger:    log.New("module", "p2p"),
		localID:   localID,
		bootnodes: cfg.Bootnodes,
	}
}

// Start begins listening for P2P connections and discovering peers.
func (n *Network) Start(ctx context.Context) error {
	ctx, n.cancel = context.WithCancel(ctx)

	addr := fmt.Sprintf("0.0.0.0:%d", n.cfg.P2PPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("p2p listen on %s: %w", addr, err)
	}
	n.listener = listener

	n.logger.Info("P2P network started",
		"listenAddr", addr,
		"localID", string(n.localID),
		"bootnodes", len(n.bootnodes),
	)

	// Accept incoming connections
	go n.acceptLoop(ctx)

	// Connect to seed/boot nodes
	go n.connectToBootnodes(ctx)

	// Periodically ping peers to check liveness
	go n.livenessLoop(ctx)

	return nil
}

// Stop shuts down the P2P network.
func (n *Network) Stop() {
	if n.cancel != nil {
		n.cancel()
	}
	if n.listener != nil {
		n.listener.Close()
	}
	n.mu.Lock()
	for _, p := range n.peers {
		if p.conn != nil {
			p.conn.Close()
		}
	}
	n.mu.Unlock()
}

// Messages returns the channel for incoming P2P messages.
func (n *Network) Messages() <-chan *Message {
	return n.msgCh
}

// Broadcast sends a message to all connected peers.
func (n *Network) Broadcast(msgType MessageType, payload []byte) error {
	n.mu.RLock()
	defer n.mu.RUnlock()

	msg := &wireMessage{
		Type:    int(msgType),
		From:    string(n.localID),
		Payload: payload,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal broadcast: %w", err)
	}

	var lastErr error
	sent := 0
	for id, peer := range n.peers {
		if !peer.IsActive || peer.conn == nil {
			continue
		}
		if err := writeFrame(peer.conn, data); err != nil {
			n.logger.Debug("Broadcast write failed", "peer", id, "err", err)
			lastErr = err
			continue
		}
		sent++
	}

	if sent > 0 {
		n.logger.Debug("Message broadcast", "type", msgType, "peers", sent)
	}

	return lastErr
}

// SendTo sends a message to a specific peer.
func (n *Network) SendTo(peerID PeerID, msgType MessageType, payload []byte) error {
	n.mu.RLock()
	peer, ok := n.peers[peerID]
	n.mu.RUnlock()

	if !ok || !peer.IsActive || peer.conn == nil {
		return fmt.Errorf("peer %s not found or inactive", peerID)
	}

	msg := &wireMessage{
		Type:    int(msgType),
		From:    string(n.localID),
		Payload: payload,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return writeFrame(peer.conn, data)
}

// PeerCount returns the number of connected active peers.
func (n *Network) PeerCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	count := 0
	for _, p := range n.peers {
		if p.IsActive {
			count++
		}
	}
	return count
}

// Peers returns a list of all connected peers.
func (n *Network) Peers() []*Peer {
	n.mu.RLock()
	defer n.mu.RUnlock()
	list := make([]*Peer, 0, len(n.peers))
	for _, p := range n.peers {
		list = append(list, &Peer{
			ID:       p.ID,
			Address:  p.Address,
			Version:  p.Version,
			IsActive: p.IsActive,
			Latency:  p.Latency,
		})
	}
	return list
}

// ── Connection management ────────────────────────────────────────────────────

func (n *Network) acceptLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := n.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return // shutting down
			}
			n.logger.Debug("Accept error", "err", err)
			continue
		}

		go n.handleInbound(ctx, conn)
	}
}

func (n *Network) handleInbound(ctx context.Context, conn net.Conn) {
	remoteAddr := conn.RemoteAddr().String()
	peerID := PeerID(fmt.Sprintf("inbound-%s", remoteAddr))

	n.mu.Lock()
	n.peers[peerID] = &Peer{
		ID:       peerID,
		Address:  remoteAddr,
		Version:  "inso/1.0",
		IsActive: true,
		Latency:  0,
		conn:     conn,
	}
	n.mu.Unlock()

	n.logger.Info("Inbound peer connected", "peer", peerID, "addr", remoteAddr)

	// Send our announce message
	announce := &wireMessage{
		Type:    int(MsgValidatorAnnounce),
		From:    string(n.localID),
		Payload: []byte(fmt.Sprintf(`{"port":%d}`, n.cfg.P2PPort)),
	}
	if data, err := json.Marshal(announce); err == nil {
		writeFrame(conn, data)
	}

	n.readLoop(ctx, peerID, conn)
}

func (n *Network) connectToBootnodes(ctx context.Context) {
	for _, addr := range n.bootnodes {
		select {
		case <-ctx.Done():
			return
		default:
		}

		go n.dialPeer(ctx, addr)
	}
}

func (n *Network) dialPeer(ctx context.Context, addr string) {
	// Retry with backoff
	for attempt := 0; attempt < 5; attempt++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			delay := time.Duration(1<<uint(attempt)) * time.Second
			n.logger.Debug("Dial peer failed, retrying", "addr", addr, "attempt", attempt+1, "delay", delay)
			time.Sleep(delay)
			continue
		}

		peerID := PeerID(fmt.Sprintf("outbound-%s", addr))

		n.mu.Lock()
		n.peers[peerID] = &Peer{
			ID:       peerID,
			Address:  addr,
			Version:  "inso/1.0",
			IsActive: true,
			conn:     conn,
		}
		n.mu.Unlock()

		n.logger.Info("Connected to peer", "peer", peerID, "addr", addr)

		// Send announce
		announce := &wireMessage{
			Type:    int(MsgValidatorAnnounce),
			From:    string(n.localID),
			Payload: []byte(fmt.Sprintf(`{"port":%d}`, n.cfg.P2PPort)),
		}
		if data, err := json.Marshal(announce); err == nil {
			writeFrame(conn, data)
		}

		n.readLoop(ctx, peerID, conn)
		return
	}

	n.logger.Warn("Failed to connect to bootnode", "addr", addr)
}

func (n *Network) readLoop(ctx context.Context, peerID PeerID, conn net.Conn) {
	defer func() {
		conn.Close()
		n.mu.Lock()
		if p, ok := n.peers[peerID]; ok {
			p.IsActive = false
		}
		n.mu.Unlock()
		n.logger.Info("Peer disconnected", "peer", peerID)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Read length-prefixed frame
		data, err := readFrame(conn)
		if err != nil {
			if ctx.Err() != nil || err == io.EOF {
				return
			}
			n.logger.Debug("Read error from peer", "peer", peerID, "err", err)
			return
		}

		var wm wireMessage
		if err := json.Unmarshal(data, &wm); err != nil {
			n.logger.Debug("Invalid message from peer", "peer", peerID, "err", err)
			continue
		}

		msg := &Message{
			Type:    MessageType(wm.Type),
			From:    PeerID(wm.From),
			Payload: wm.Payload,
		}

		select {
		case n.msgCh <- msg:
		default:
			n.logger.Warn("Message channel full, dropping message", "type", msg.Type)
		}
	}
}

func (n *Network) livenessLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.checkPeerLiveness()
		}
	}
}

func (n *Network) checkPeerLiveness() {
	n.mu.Lock()
	defer n.mu.Unlock()

	for id, peer := range n.peers {
		if !peer.IsActive || peer.conn == nil {
			continue
		}
		// Try a zero-byte write to detect broken connections
		peer.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := peer.conn.Write(nil); err != nil {
			n.logger.Debug("Peer liveness check failed", "peer", id)
			peer.IsActive = false
			peer.conn.Close()
		}
		peer.conn.SetWriteDeadline(time.Time{}) // reset
	}
}

// ── Length-prefixed framing ──────────────────────────────────────────────────

// writeFrame writes a length-prefixed message to conn.
func writeFrame(conn net.Conn, data []byte) error {
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetWriteDeadline(time.Time{})

	// 4-byte big-endian length prefix
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := conn.Write(lenBuf); err != nil {
		return err
	}
	_, err := conn.Write(data)
	return err
}

// readFrame reads a length-prefixed message from conn.
func readFrame(conn net.Conn) ([]byte, error) {
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	defer conn.SetReadDeadline(time.Time{})

	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(lenBuf)
	if length > 10*1024*1024 { // 10MB max message
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}

	return data, nil
}
