package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	fmt.Printf("InSo Validator %s\n", version)
	fmt.Println("Starting InSoBlok L2 Validator Node...")

	// TODO: Initialize config
	// TODO: Initialize validator keys
	// TODO: Initialize P2P networking (libp2p)
	// TODO: Initialize block sync engine
	// TODO: Initialize state verification engine
	// TODO: Initialize TasteScore-weighted consensus
	// TODO: Initialize staking & delegation manager
	// TODO: Initialize slashing monitor
	// TODO: Start validator RPC server

	fmt.Println("Validator is not yet implemented. See README.md for architecture.")
	os.Exit(0)
}
