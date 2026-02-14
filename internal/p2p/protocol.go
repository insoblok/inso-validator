package p2p

import (
	"encoding/json"
	"fmt"
)

// ── Gossip protocol message payloads ─────────────────────────────────────────

// BlockAnnounce is the payload for MsgNewBlock.
type BlockAnnounce struct {
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

// AttestationMsg is the payload for MsgAttestation.
type AttestationMsg struct {
	BlockNumber uint64 `json:"blockNumber"`
	BlockHash   string `json:"blockHash"`
	Validator   string `json:"validator"`
	TasteScore  float64 `json:"tasteScore"`
	Signature   []byte `json:"signature"`
	Timestamp   int64  `json:"timestamp"`
}

// SyncRequestMsg is the payload for MsgSyncRequest.
type SyncRequestMsg struct {
	FromBlock uint64 `json:"fromBlock"`
	ToBlock   uint64 `json:"toBlock"`
}

// SyncResponseMsg is the payload for MsgSyncResponse.
type SyncResponseMsg struct {
	Blocks []BlockAnnounce `json:"blocks"`
}

// ValidatorAnnounceMsg is the payload for MsgValidatorAnnounce.
type ValidatorAnnounceMsg struct {
	Address   string `json:"address"`
	Port      int    `json:"port"`
	Version   string `json:"version"`
	ChainID   uint64 `json:"chainId"`
	Stake     string `json:"stake"` // decimal string
}

// SlashReportMsg is the payload for MsgSlashReport.
type SlashReportMsg struct {
	Validator string `json:"validator"`
	Reason    string `json:"reason"`
	Evidence  []byte `json:"evidence"`
}

// ── Encoding helpers ─────────────────────────────────────────────────────────

// EncodePayload serializes a gossip protocol payload to JSON bytes.
func EncodePayload(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// DecodeBlockAnnounce deserializes a MsgNewBlock payload.
func DecodeBlockAnnounce(data []byte) (*BlockAnnounce, error) {
	var msg BlockAnnounce
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("decode BlockAnnounce: %w", err)
	}
	return &msg, nil
}

// DecodeAttestationMsg deserializes a MsgAttestation payload.
func DecodeAttestationMsg(data []byte) (*AttestationMsg, error) {
	var msg AttestationMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("decode AttestationMsg: %w", err)
	}
	return &msg, nil
}

// DecodeSyncRequest deserializes a MsgSyncRequest payload.
func DecodeSyncRequest(data []byte) (*SyncRequestMsg, error) {
	var msg SyncRequestMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("decode SyncRequestMsg: %w", err)
	}
	return &msg, nil
}

// DecodeValidatorAnnounce deserializes a MsgValidatorAnnounce payload.
func DecodeValidatorAnnounce(data []byte) (*ValidatorAnnounceMsg, error) {
	var msg ValidatorAnnounceMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("decode ValidatorAnnounceMsg: %w", err)
	}
	return &msg, nil
}
