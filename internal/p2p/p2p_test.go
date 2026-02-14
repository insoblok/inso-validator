package p2p

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/insoblok/inso-validator/internal/config"
)

func newTestNetwork(port int) *Network {
	cfg := &config.ValidatorConfig{
		P2PPort:   port,
		DataDir:   "/tmp/test-p2p",
		Bootnodes: []string{},
	}
	return NewNetwork(cfg)
}

func TestNetworkStartStop(t *testing.T) {
	n := newTestNetwork(0) // 0 = pick random port
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use port 0 for random assignment
	n.cfg.P2PPort = 0
	err := n.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start network: %v", err)
	}
	defer n.Stop()

	if n.PeerCount() != 0 {
		t.Errorf("Expected 0 peers at start, got %d", n.PeerCount())
	}
}

func TestPeerConnectivity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create two networks on different random ports
	n1cfg := &config.ValidatorConfig{P2PPort: 0, DataDir: "/tmp/test-p2p1", Bootnodes: []string{}}
	n1 := NewNetwork(n1cfg)

	if err := n1.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer n1.Stop()

	// Get the actual listen address of n1
	n1Addr := n1.listener.Addr().String()

	n2cfg := &config.ValidatorConfig{P2PPort: 0, DataDir: "/tmp/test-p2p2", Bootnodes: []string{n1Addr}}
	n2 := NewNetwork(n2cfg)

	if err := n2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer n2.Stop()

	// Wait for n2 to connect to n1
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("Timeout waiting for peer connection. n1 peers: %d, n2 peers: %d",
				n1.PeerCount(), n2.PeerCount())
		default:
			if n1.PeerCount() >= 1 && n2.PeerCount() >= 1 {
				t.Logf("Peers connected: n1=%d, n2=%d", n1.PeerCount(), n2.PeerCount())
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func TestBroadcastMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up two connected networks
	n1cfg := &config.ValidatorConfig{P2PPort: 0, DataDir: "/tmp/test-p2p3", Bootnodes: []string{}}
	n1 := NewNetwork(n1cfg)
	if err := n1.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer n1.Stop()

	n1Addr := n1.listener.Addr().String()

	n2cfg := &config.ValidatorConfig{P2PPort: 0, DataDir: "/tmp/test-p2p4", Bootnodes: []string{n1Addr}}
	n2 := NewNetwork(n2cfg)
	if err := n2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer n2.Stop()

	// Wait for connection
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("Timeout waiting for peer connection")
		default:
			if n1.PeerCount() >= 1 {
				goto connected
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
connected:

	// Broadcast a block announce from n1
	announce := &BlockAnnounce{
		Number:    42,
		Hash:      "0xdeadbeef",
		StateRoot: "0xcafebabe",
		GasUsed:   21000,
		TxCount:   1,
	}
	payload, _ := json.Marshal(announce)

	err := n1.Broadcast(MsgNewBlock, payload)
	if err != nil {
		t.Fatalf("Broadcast failed: %v", err)
	}

	// n2 should receive it (drain any announce messages first)
	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg := <-n2.Messages():
			if msg.Type == MsgValidatorAnnounce {
				// Skip handshake announces
				continue
			}
			if msg.Type != MsgNewBlock {
				t.Errorf("Expected MsgNewBlock, got %d", msg.Type)
			}
			var decoded BlockAnnounce
			if err := json.Unmarshal(msg.Payload, &decoded); err != nil {
				t.Fatalf("Decode payload failed: %v", err)
			}
			if decoded.Number != 42 {
				t.Errorf("Expected block 42, got %d", decoded.Number)
			}
			t.Logf("Message received: block %d from %s", decoded.Number, msg.From)
			return
		case <-timeout:
			t.Fatal("Timeout waiting for broadcast message")
		}
	}
}

func TestProtocolEncodeDecode(t *testing.T) {
	// Block announce
	announce := &BlockAnnounce{
		Number:     100,
		Hash:       "0xabcdef",
		ParentHash: "0x123456",
		Timestamp:  1234567890,
		StateRoot:  "0xdeadbeef",
		GasUsed:    42000,
		GasLimit:   30000000,
		TxCount:    5,
		Sequencer:  "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
	}

	data, err := EncodePayload(announce)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeBlockAnnounce(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.Number != announce.Number {
		t.Errorf("Number mismatch: %d != %d", decoded.Number, announce.Number)
	}
	if decoded.Hash != announce.Hash {
		t.Errorf("Hash mismatch")
	}
	if decoded.GasUsed != announce.GasUsed {
		t.Errorf("GasUsed mismatch")
	}

	// Attestation
	att := &AttestationMsg{
		BlockNumber: 100,
		BlockHash:   "0xabcdef",
		Validator:   "0x90F79bf6EB2c4f870365E785982E1f101E93b906",
		TasteScore:  0.85,
		Signature:   []byte{0x01, 0x02, 0x03},
		Timestamp:   time.Now().Unix(),
	}

	attData, _ := EncodePayload(att)
	decodedAtt, err := DecodeAttestationMsg(attData)
	if err != nil {
		t.Fatal(err)
	}
	if decodedAtt.BlockNumber != att.BlockNumber {
		t.Error("Attestation block number mismatch")
	}
	if decodedAtt.TasteScore != att.TasteScore {
		t.Error("Attestation taste score mismatch")
	}
}

func TestFrameProtocol(t *testing.T) {
	// Test the length-prefixed framing via a connected pair
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1cfg := &config.ValidatorConfig{P2PPort: 0, DataDir: "/tmp/test-p2p5", Bootnodes: []string{}}
	n1 := NewNetwork(n1cfg)
	if err := n1.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer n1.Stop()

	n1Addr := n1.listener.Addr().String()
	n2cfg := &config.ValidatorConfig{P2PPort: 0, DataDir: "/tmp/test-p2p6", Bootnodes: []string{n1Addr}}
	n2 := NewNetwork(n2cfg)
	if err := n2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer n2.Stop()

	// Wait for connection
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("Timeout")
		default:
			if n1.PeerCount() >= 1 {
				goto ready
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
ready:

	// Send multiple messages rapidly to test framing
	for i := 0; i < 10; i++ {
		payload, _ := json.Marshal(&BlockAnnounce{Number: uint64(i + 1)})
		n1.Broadcast(MsgNewBlock, payload)
	}

	received := 0
	timeout := time.After(5 * time.Second)
	for received < 10 {
		select {
		case msg := <-n2.Messages():
			if msg.Type == MsgValidatorAnnounce {
				continue // skip handshake
			}
			received++
		case <-timeout:
			t.Fatalf("Only received %d/10 messages", received)
		}
	}
	t.Logf("All 10 framed messages received")
}
