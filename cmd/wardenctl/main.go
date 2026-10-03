// Command wardenctl is the operator CLI. v1 implements the audit-log
// verification command the spec calls for; policy validate/diff/apply
// (also named in the spec as wardenctl's job) are tracked as follow-up
// work once the console exists to drive them against, not silently
// dropped — see docs/MILESTONES.md.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/audit"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "audit":
		runAudit(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `wardenctl audit verify [--from-checkpoint]

  --from-checkpoint   verify from the latest signed checkpoint forward
                       instead of from genesis (requires WARDEN_AUDIT_PUBLIC_KEY)

Environment:
  DATABASE_URL             Postgres DSN (default: postgres://warden:warden@localhost:5432/warden?sslmode=disable)
  WARDEN_AUDIT_PUBLIC_KEY  hex-encoded Ed25519 public key, required for --from-checkpoint`)
}

func runAudit(args []string) {
	if len(args) < 1 || args[0] != "verify" {
		usage()
		os.Exit(2)
	}
	fromCheckpoint := false
	for _, a := range args[1:] {
		if a == "--from-checkpoint" {
			fromCheckpoint = true
		}
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://warden:warden@localhost:5432/warden?sslmode=disable"
	}
	conn, err := db.Connect(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to database: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx := context.Background()
	var result audit.VerifyResult

	if fromCheckpoint {
		pubHex := os.Getenv("WARDEN_AUDIT_PUBLIC_KEY")
		if pubHex == "" {
			fmt.Fprintln(os.Stderr, "--from-checkpoint requires WARDEN_AUDIT_PUBLIC_KEY")
			os.Exit(2)
		}
		pubBytes, err := hex.DecodeString(pubHex)
		if err != nil || len(pubBytes) != ed25519.PublicKeySize {
			fmt.Fprintf(os.Stderr, "invalid WARDEN_AUDIT_PUBLIC_KEY: %v\n", err)
			os.Exit(2)
		}
		result, err = audit.VerifyFromLatestCheckpoint(ctx, conn, ed25519.PublicKey(pubBytes))
		if err != nil {
			fmt.Fprintf(os.Stderr, "verify: %v\n", err)
			os.Exit(1)
		}
	} else {
		result, err = audit.VerifyFull(ctx, conn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "verify: %v\n", err)
			os.Exit(1)
		}
	}

	if result.OK {
		fmt.Printf("OK: verified %d record(s) (seq %d onward) in %s\n",
			result.RecordsVerified, result.StartSeq, result.Duration)
		os.Exit(0)
	}

	fmt.Printf("TAMPERED: chain broken at seq %d: %s\n", result.FailureAt, result.FailureReason)
	fmt.Printf("(%d record(s) verified cleanly before the break, in %s)\n", result.RecordsVerified, result.Duration)
	os.Exit(1)
}
