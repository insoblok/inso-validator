package consensus

import (
	"crypto/ecdsa"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-validator/internal/config"
)

// helper: generate a test key and address
func testKey(t *testing.T) (*ecdsa.PrivateKey, common.Address) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key, crypto.PubkeyToAddress(key.PublicKey)
}

func TestSignAndVerifyAttestation(t *testing.T) {
	key, addr := testKey(t)

	eng := &Engine{
		privateKey:   key,
		localAddr:    addr,
		validators:   map[common.Address]*ValidatorInfo{addr: {Address: addr, IsActive: true}},
		attestations: make(map[uint64][]*Attestation),
	}

	att := &Attestation{
		BlockNumber: 42,
		BlockHash:   common.HexToHash("0xdeadbeef"),
		Validator:   addr,
		Timestamp:   time.Now(),
	}
	att.Signature = eng.signAttestation(att)

	if len(att.Signature) != 65 {
		t.Fatalf("expected 65-byte signature, got %d", len(att.Signature))
	}

	if err := eng.VerifyAttestation(att); err != nil {
		t.Fatalf("valid attestation should verify: %v", err)
	}
}

func TestVerifyAttestation_WrongSigner(t *testing.T) {
	key1, addr1 := testKey(t)
	_, addr2 := testKey(t)

	eng := &Engine{
		privateKey: key1,
		localAddr:  addr1,
		validators: map[common.Address]*ValidatorInfo{
			addr1: {Address: addr1, IsActive: true},
			addr2: {Address: addr2, IsActive: true},
		},
		attestations: make(map[uint64][]*Attestation),
	}

	att := &Attestation{
		BlockNumber: 1,
		BlockHash:   common.HexToHash("0xaabbcc"),
		Validator:   addr2, // claim to be addr2 but sign with key1
		Timestamp:   time.Now(),
	}
	att.Signature = eng.signAttestation(att)

	// Recovered address will be addr1, not addr2 → invalid
	if err := eng.VerifyAttestation(att); err != ErrInvalidSignature {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestVerifyAttestation_UnknownValidator(t *testing.T) {
	key, addr := testKey(t)

	eng := &Engine{
		privateKey:   key,
		localAddr:    addr,
		validators:   make(map[common.Address]*ValidatorInfo), // empty: no validators registered
		attestations: make(map[uint64][]*Attestation),
	}

	att := &Attestation{
		BlockNumber: 5,
		BlockHash:   common.HexToHash("0x1234"),
		Validator:   addr,
		Timestamp:   time.Now(),
	}
	att.Signature = eng.signAttestation(att)

	if err := eng.VerifyAttestation(att); err != ErrUnknownValidator {
		t.Fatalf("expected ErrUnknownValidator, got %v", err)
	}
}

func TestVerifyAttestation_TruncatedSignature(t *testing.T) {
	_, addr := testKey(t)

	eng := &Engine{
		validators:   map[common.Address]*ValidatorInfo{addr: {Address: addr, IsActive: true}},
		attestations: make(map[uint64][]*Attestation),
	}

	att := &Attestation{
		BlockNumber: 1,
		BlockHash:   common.HexToHash("0xaa"),
		Validator:   addr,
		Signature:   []byte{0x01, 0x02, 0x03}, // too short
		Timestamp:   time.Now(),
	}

	if err := eng.VerifyAttestation(att); err != ErrInvalidSignature {
		t.Fatalf("expected ErrInvalidSignature for short sig, got %v", err)
	}
}

func TestVerifyAttestation_TamperedBlockNumber(t *testing.T) {
	key, addr := testKey(t)

	eng := &Engine{
		privateKey:   key,
		localAddr:    addr,
		validators:   map[common.Address]*ValidatorInfo{addr: {Address: addr, IsActive: true}},
		attestations: make(map[uint64][]*Attestation),
	}

	att := &Attestation{
		BlockNumber: 100,
		BlockHash:   common.HexToHash("0xfeedface"),
		Validator:   addr,
		Timestamp:   time.Now(),
	}
	att.Signature = eng.signAttestation(att)

	// Tamper with block number
	att.BlockNumber = 101

	if err := eng.VerifyAttestation(att); err != ErrInvalidSignature {
		t.Fatalf("expected ErrInvalidSignature for tampered data, got %v", err)
	}
}

func TestAttestationHashDeterministic(t *testing.T) {
	att := &Attestation{
		BlockNumber: 999,
		BlockHash:   common.HexToHash("0xabcdef"),
		Validator:   common.HexToAddress("0x1111111111111111111111111111111111111111"),
	}

	h1 := attestationHash(att)
	h2 := attestationHash(att)

	if h1 != h2 {
		t.Fatal("attestation hash must be deterministic")
	}
}

func TestRegisterValidatorSovereigntyWeight(t *testing.T) {
	key, addr := testKey(t)

	eng := &Engine{
		privateKey:   key,
		localAddr:    addr,
		cfg:          &config.ConsensusConfig{BlockTimeout: time.Second, AttestationDelay: time.Millisecond},
		validators:   make(map[common.Address]*ValidatorInfo),
		attestations: make(map[uint64][]*Attestation),
		logger:       log.New("module", "test"),
	}

	stake := new(big.Int).Mul(big.NewInt(100), big.NewInt(1e18)) // 100 INSO
	eng.RegisterValidator(addr, stake, 0.85)

	v := eng.validators[addr]
	if v == nil {
		t.Fatal("validator not registered")
	}
	if v.VotePower <= 0 {
		t.Fatalf("expected positive vote power, got %f", v.VotePower)
	}
	if !v.IsActive {
		t.Fatal("validator should be active")
	}
}

func TestIsFinalized(t *testing.T) {
	key1, addr1 := testKey(t)
	key2, addr2 := testKey(t)
	key3, addr3 := testKey(t)

	eng := &Engine{
		privateKey:   key1,
		localAddr:    addr1,
		cfg:          &config.ConsensusConfig{BlockTimeout: time.Second, AttestationDelay: time.Millisecond},
		validators:   make(map[common.Address]*ValidatorInfo),
		attestations: make(map[uint64][]*Attestation),
		logger:       log.New("module", "test"),
	}

	// Register 3 validators with equal stake
	stake := new(big.Int).Mul(big.NewInt(100), big.NewInt(1e18))
	eng.RegisterValidator(addr1, stake, 0.5)
	eng.RegisterValidator(addr2, stake, 0.5)
	eng.RegisterValidator(addr3, stake, 0.5)

	blockNum := uint64(10)
	blockHash := common.HexToHash("0xdeadbeef")

	// 0/3 attestations → not finalized
	if eng.IsFinalized(blockNum) {
		t.Fatal("should not be finalized with no attestations")
	}

	// 1/3 attestation → not finalized
	att1 := &Attestation{BlockNumber: blockNum, BlockHash: blockHash, Validator: addr1, Timestamp: time.Now()}
	att1.Signature = func() []byte { sig, _ := crypto.Sign(attestationHash(att1).Bytes(), key1); return sig }()
	eng.attestations[blockNum] = append(eng.attestations[blockNum], att1)
	if eng.IsFinalized(blockNum) {
		t.Fatal("should not be finalized with 1/3 attestations")
	}

	// 2/3 attestations → not finalized (need >2/3)
	att2 := &Attestation{BlockNumber: blockNum, BlockHash: blockHash, Validator: addr2, Timestamp: time.Now()}
	att2.Signature = func() []byte { sig, _ := crypto.Sign(attestationHash(att2).Bytes(), key2); return sig }()
	eng.attestations[blockNum] = append(eng.attestations[blockNum], att2)
	// 2/3 = 0.666... which is NOT > 2/3
	if eng.IsFinalized(blockNum) {
		t.Fatal("should not be finalized with exactly 2/3 attestations")
	}

	// 3/3 attestations → finalized
	att3 := &Attestation{BlockNumber: blockNum, BlockHash: blockHash, Validator: addr3, Timestamp: time.Now()}
	att3.Signature = func() []byte { sig, _ := crypto.Sign(attestationHash(att3).Bytes(), key3); return sig }()
	eng.attestations[blockNum] = append(eng.attestations[blockNum], att3)
	if !eng.IsFinalized(blockNum) {
		t.Fatal("should be finalized with 3/3 attestations")
	}
}
