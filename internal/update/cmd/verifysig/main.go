// Command verifysig checks a release signature with the same code the recorder
// uses to check one.
//
// It exists so `make release` can prove the signature it just made is one that
// will actually be accepted. Signing and verifying with different
// implementations - openssl on one side, crypto/ed25519 on the other - can
// disagree in ways that are invisible until an update is refused in the field,
// and the cheapest place to find that out is the machine that made it.
package main

import (
	"fmt"
	"os"

	"github.com/OmarAlghafri/netrewind/internal/update"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: verifysig <public-key-base64> <signed-file> <signature-file>")
		os.Exit(2)
	}
	pub, signedPath, sigPath := os.Args[1], os.Args[2], os.Args[3]

	signed, err := os.ReadFile(signedPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verifysig:", err)
		os.Exit(1)
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verifysig:", err)
		os.Exit(1)
	}

	if err := update.VerifySignature(pub, signed, sig); err != nil {
		fmt.Fprintln(os.Stderr, "verifysig:", err)
		fmt.Fprintln(os.Stderr, "the signature would be refused by every recorder configured with this key")
		os.Exit(1)
	}
	fmt.Printf("signature over %s verifies against the configured key\n", signedPath)
}
