package p2p

import (
	"context"
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-validator/internal/config"
)

// PeerID is a unique identifier for a peer on the network.
type PeerID string

// Peer represents a connected peer.
type Peer struct {
	ID        PeerID `json:"id"`
	Address   string `json:"address"`
	Version   string `json:"version"`
	IsActive  bool   `json:"isActive"`
	Latency   int64  `json:"latencyMs"`
}

// Message represents a P2P protocol message.
type Message struct {
	Type    MessageType
	From    PeerID
	Payload []byte
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

// Network manages the P2P overlay network for validators.
type Network struct {
	mu       sync.RWMutex
	cfg      *config.ValidatorConfig
	peers    map[PeerID]*Peer
	msgCh    chan *Message
	logger   log.Logger
	cancel   context.CancelFunc
}

// NewNetwork creates a new P2P network manager.
func NewNetwork(cfg *config.ValidatorConfig) *Network {
	return &Network{
		cfg:   cfg,
		peers: make(map[PeerID]*Peer),
		msgCh: make(chan *Message, 1024),
		logger: log.New("module", "p2p"),
	}
}

// Start begins listening for P2P connections and discovering peers.
func (n *Network) Start(ctx context.Context) error {
	ctx, n.cancel = context.WithCancel(ctx)

	n.logger.Info("P2P network starting",
		"port", n.cfg.P2PPort,
		"datadir", n.cfg.DataDir,
	)

	// In production, this would start a libp2p host with:
	// - mDNS discovery for local devnet
	// - DHT/Kademlia for mainnet peer discovery
	// - Gossipsub for block and attestation propagation
	// - Noise protocol for encrypted transport

	go n.discoveryLoop(ctx)
	go n.messageLoop(ctx)

	n.logger.Info("P2P network started")
	return nil
}

// Stop shuts down the P2P network.
func (n *Network) Stop() {
	if n.cancel != nil {
		n.cancel()
	}
	close(n.msgCh)
}

// Messages returns the channel for incoming P2P messages.
func (n *Network) Messages() <-chan *Message {
	return n.msgCh
}

// Broadcast sends a message to all connected peers.
func (n *Network) Broadcast(msgType MessageType, payload []byte) error {
	n.mu.RLock()
	defer n.mu.RUnlock()

	for id, peer := range n.peers {
		if !peer.IsActive {
			continue
		}
		n.logger.Debug("Broadcasting message", "type", msgType, "to", id)
		// In production: serialize and send via libp2p stream
	}

	return nil
}

// SendTo sends a message to a specific peer.
func (n *Network) SendTo(peerID PeerID, msgType MessageType, payload []byte) error {
	n.mu.RLock()
	peer, ok := n.peers[peerID]
	n.mu.RUnlock()

	if !ok || !peer.IsActive {
		return fmt.Errorf("peer %s not found or inactive", peerID)
	}

	n.logger.Debug("Sending message", "type", msgType, "to", peerID)
	return nil
}

// PeerCount returns the number of connected peers.
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
		list = append(list, p)
	}
	return list
}

func (n *Network) discoveryLoop(ctx context.Context) {
	// Placeholder: In production, continuously discover new peers
	// via mDNS (devnet) or DHT (mainnet).
	<-ctx.Done()
}

func (n *Network) messageLoop(ctx context.Context) {
	// Placeholder: In production, read from libp2p streams
	// and dispatch to the message channel.
	<-ctx.Done()
}
