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

- **TasteScore-Weighted Consensus** — Validator influence proportional to staked INSO + TasteScore
- **State Verification** — Re-executes transactions to verify sequencer-produced state roots
- **Slashing Protection** — Built-in double-signing detection and slashing enforcement
- **P2P Networking** — libp2p-based gossip for block propagation and validator communication
- **XP Reputation** — Earns XP for honest validation, loses XP for missed blocks or misbehavior
- **Delegation Support** — Accepts delegated stake from token holders

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
│   ├── config/             # Configuration loading
│   ├── consensus/          # TasteScore-weighted PoS consensus
│   ├── p2p/                # libp2p networking layer
│   ├── sync/               # Block sync from sequencer & L1
│   ├── verifier/           # Block & state root verification
│   ├── staking/            # Stake management & delegation
│   ├── slashing/           # Slashing detection & enforcement
│   ├── tastescore/         # TasteScore client & caching
│   └── state/              # Local state management
├── pkg/
│   └── types/              # Shared types
├── scripts/
├── docker/
├── docs/
├── Makefile
├── Dockerfile
├── go.mod
├── go.sum
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
