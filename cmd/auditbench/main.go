// Command auditbench seeds N real audit records through the actual
// ChainWriter (not a synthetic bulk insert that skips the real hashing
// path) and times full-chain vs. checkpoint-fast-path verification —
// the spec's "time to verify N million records" benchmark target, which
// milestone 7 only ever measured at 5 real records.
//
// Run against a dedicated database, not the dev "warden" DB — this
// truncates audit_events first. Default DSN points at warden_test, the
// same isolated database internal/audit's own tests use.
package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/audit"
	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/db"
	"github.com/google/uuid"
)

func main() {
	n := flag.Int("n", 1_000_000, "number of audit records to seed")
	dsn := flag.String("dsn", "postgres://warden:warden@localhost:5432/warden_test?sslmode=disable", "Postgres DSN (defaults to the isolated test DB, not the dev gateway's own)")
	flag.Parse()

	conn, err := db.Connect(*dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx := context.Background()
	reset(ctx, conn)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate key: %v\n", err)
		os.Exit(1)
	}

	// Append() only ever touches w.db, never Kafka — these are just to
	// satisfy NewChainWriter's constructor, which validates them even
	// though this program never calls Run() (no need for a real broker).
	writer, err := audit.NewChainWriter(conn, []string{"localhost:9092"}, "unused")
	if err != nil {
		fmt.Fprintf(os.Stderr, "new chain writer: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("seeding %d records...\n", *n)
	seedStart := time.Now()
	for i := 0; i < *n; i++ {
		e := audit.Event{
			EventID: uuid.NewString(), Decision: "allow", Reason: "policy0",
			AgentID: "agent-demo", ActingAs: "user-1", Action: "CallModel",
			ResourceType: "Model", ResourceID: "mock-model", OccurredAt: time.Now().UTC(),
		}
		if err := writer.Append(ctx, e); err != nil {
			fmt.Fprintf(os.Stderr, "append %d: %v\n", i, err)
			os.Exit(1)
		}
		if (i+1)%50_000 == 0 {
			fmt.Printf("  %d/%d seeded (%s elapsed)\n", i+1, *n, time.Since(seedStart))
		}
	}
	seedDuration := time.Since(seedStart)
	fmt.Printf("seeded %d records in %s (%.0f records/sec)\n\n", *n, seedDuration, float64(*n)/seedDuration.Seconds())

	checkpointer := audit.NewCheckpointer(conn, priv)
	if err := checkpointer.CreateCheckpoint(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "create checkpoint: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("created a checkpoint at the current tip")

	fmt.Println("\nrunning full-chain verification (from genesis)...")
	full, err := audit.VerifyFull(ctx, conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify full: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  OK=%v, %d records verified, took %s\n", full.OK, full.RecordsVerified, full.Duration)

	fmt.Println("\nrunning checkpoint fast-path verification...")
	fast, err := audit.VerifyFromLatestCheckpoint(ctx, conn, pub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify from checkpoint: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  OK=%v, %d records verified, took %s\n", fast.OK, fast.RecordsVerified, fast.Duration)

	fmt.Printf("\n=== RESULT ===\n")
	fmt.Printf("%d records seeded in %s (%.0f/sec)\n", *n, seedDuration, float64(*n)/seedDuration.Seconds())
	fmt.Printf("Full-chain verify:      %s (%d records)\n", full.Duration, full.RecordsVerified)
	fmt.Printf("Checkpoint fast-path:   %s (%d records)\n", fast.Duration, fast.RecordsVerified)
}

func reset(ctx context.Context, conn *sql.DB) {
	conn.ExecContext(ctx, `ALTER TABLE audit_events ENABLE TRIGGER audit_events_no_update`)
	conn.ExecContext(ctx, `ALTER TABLE audit_events ENABLE TRIGGER audit_events_no_delete`)
	conn.ExecContext(ctx, `TRUNCATE audit_events, audit_checkpoints`)
	conn.ExecContext(ctx, `UPDATE audit_chain_state SET last_seq = 0, last_hash = '' WHERE id = 1`)
}
