# InSo Validator

Validator node for the InSoBlok L2 blockchain. Validates blocks produced by the sequencer, participates in TasteScore-weighted consensus, and verifies state transitions.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                   InSo Validator                     │
├──────────────┬──────────────┬───────────────────────┤
│  P2P Network │  Sync Engine │  Consensus Engine      │
│  (libp2p)    │  (L1 + P2P)  │  (TasteScore PoS)     │
├──────────────┴──────────────┴───────────────────────┤
│              Verification Engine                     │
│  ┌────────────┐  ┌──────────────┐  ┌──────────────┐ │
│  │  Block      │  │ State Root   │  │ TasteScore   │ │
│  │  Validation │  │ Verification │  │  Staking     │ │
│  └────────────┘  └──────────────┘  └──────────────┘ │
├─────────────────────────────────────────────────────┤
│                    EVM Engine                         │
│              (go-ethereum fork)                       │
└─────────────────────────────────────────────────────┘
```

## Features

- **Sovereignty-Weighted PoS Consensus** — Voting power = stake × (1 + 0.5 × sovereigntyScore)
- **State Verification** — Re-executes state root derivation to verify sequencer blocks
- **Adaptive Block Validation** — Verifies block gas limits/tx counts are within adaptive bounds (15M–60M gas, 1K–10K txs)
- **Lane Allocation Checks** — Validates that execution lane stats are internally consistent
- **Compute Receipt Verification** — Independently recomputes SHA-256 receipt hashes to verify integrity
- **ECDSA Attestation Signing** — secp256k1 attestation signatures with on-chain signature recovery
- **Slashing Protection** — Double-signing detection and slashing enforcement
- **P2P Networking** — Gossip-based block propagation + attestation relay
- **XP Reputation System** — Earns XP for attestations (+10), uptime (+5); loses XP for slashes (-500)
- **Sovereignty Scoring** — 5-factor composite (stake 30%, TasteScore 25%, XP 20%, uptime 15%, DID age 10%)
- **Prometheus Metrics** — Full observability with 16 metric counters/gauges on `:6061/metrics`

## Prerequisites

- Go 1.22+
- Docker (optional)
- Make
- Minimum 8GB RAM, 4 CPU cores (recommended for mainnet)

## Quick Start

### Build from source
```bash
make build
```

### Initialize validator keys
```bash
./bin/inso-validator init --datadir ~/.inso-validator
```

### Register as validator
```bash
./bin/inso-validator register \
  --stake 10000 \
  --rpc-url https://rpc.insoblokai.io
```

### Run validator node
```bash
make run
```

### Run with Docker
```bash
docker build -t inso-validator .
docker run -v ~/.inso-validator:/data -p 30303:30303 -p 8547:8547 inso-validator
```

### Run tests
```bash
make test
```

## Configuration

```yaml
# config.yaml
validator:
  datadir: "/data"
  listen_addr: "0.0.0.0:8547"
  p2p_port: 30303
  min_stake: 10000

sequencer:
  rpc_url: "http://sequencer:8545"
  ws_url: "ws://sequencer:8546"

l1:
  rpc_url: "https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY"
  chain_id: 1
  staking_contract: "0x..."

tastescore:
  api_url: "https://api.insoblokai.io"
  api_key: "${TASTESCORE_API_KEY}"

consensus:
  block_timeout: 2s
  attestation_delay: 200ms
  slashing_enabled: true

sovereignty:
  enabled: true
  xp_decay_rate: 0.005
  attestation_xp: 10
  uptime_bonus_xp: 5
  slash_penalty_xp: 500

adaptive_block:
  enabled: true
  min_gas_limit: 15000000
  max_gas_limit: 60000000
  min_max_tx: 1000
  max_max_tx: 10000

logging:
  level: info
  format: json

metrics:
  enabled: true
  addr: "0.0.0.0:6061"
```

| Variable | Default | Description |
|----------|---------|-------------|
| `INSO_DATADIR` | `~/.inso-validator` | Validator data directory |
| `INSO_VALIDATOR_KEY` | — | Validator signing private key |
| `INSO_SEQUENCER_URL` | — | Sequencer RPC endpoint |
| `INSO_L1_RPC_URL` | — | L1 Ethereum RPC endpoint |
| `INSO_TASTESCORE_API_KEY` | — | TasteScore API key |
| `INSO_MIN_STAKE` | `10000` | Minimum INSO stake to validate |

## Project Structure

```
inso-validator/
├── cmd/
│   └── validator/          # Main entry point
│       └── main.go
├── internal/
│   ├── config/             # Configuration & YAML loading
│   ├── consensus/          # Sovereignty-weighted PoS consensus engine
│   ├── metrics/            # Prometheus metrics (16 counters/gauges)
│   ├── p2p/                # P2P networking (gossip, block announce, attestations)
│   ├── reputation/         # XP reputation manager & sovereignty scoring
│   ├── rpc/                # JSON-RPC server (validator status, validators, peers, stakes)
│   ├── staking/            # Stake management & delegation
│   ├── sync/               # Block sync from sequencer (P2P + RPC fallback)
│   └── verification/       # Block verification engine
│       ├── engine.go        #   State root re-derivation
│       └── features.go      #   Lane allocation, compute receipts, adaptive block checks
├── config.yaml
├── Makefile
├── Dockerfile
├── go.mod
└── README.md
```

## Validator Operations

### Check validator status
```bash
./bin/inso-validator status --rpc-url https://rpc.insoblokai.io
```

### Withdraw stake
```bash
./bin/inso-validator withdraw --amount 5000 --rpc-url https://rpc.insoblokai.io
```

### View earnings
```bash
./bin/inso-validator rewards --rpc-url https://rpc.insoblokai.io
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache-2.0
