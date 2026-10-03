// Command genkey is a one-shot helper for generate_env_secrets.sh: prints
// a fresh Ed25519 seed and its public key as hex, space-separated.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err)
	}
	seed := priv.Seed()
	fmt.Printf("%s %s\n", hex.EncodeToString(seed), hex.EncodeToString(pub))
}
