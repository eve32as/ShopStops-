// shopstops-migrate is a separate operator-run process, never API startup.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eve32as/ShopStops-/internal/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, getenv func(string) string, out, stderr io.Writer) int {
	var o migrations.Options
	f := flag.NewFlagSet("shopstops-migrate", flag.ContinueOnError)
	f.SetOutput(io.Discard) // flag errors may echo submitted secrets; never print them.
	f.StringVar(&o.Mode, "mode", "plan", "plan (read-only default) or apply")
	f.StringVar(&o.Directory, "migrations-dir", "db/migrations", "reviewed migration directory")
	f.StringVar(&o.ExpectedDatabase, "expected-database", "", "required actual current_database for apply")
	f.StringVar(&o.BackupReference, "backup-reference", "", "non-secret operator evidence label; NOT verified by this tool")
	f.StringVar(&o.RestoreTestReference, "restore-test-reference", "", "non-secret restore rehearsal label; NOT verified by this tool")
	f.BoolVar(&o.AllowLocalTest, "allow-local-test", false, "ONLY local disposable shopstops_ci database/user; permits disabled TLS")
	f.DurationVar(&o.Timeout, "timeout", 15*time.Minute, "whole-run execution deadline, 1s..2h (default 15m)")
	if err := f.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprintln(out, "One-shot forward migrations. DATABASE_URL must be supplied privately in the environment.\nDefault plan creates no ledger or schema objects. Existing populated databases without a ledger require independent adoption review; no baseline/stamp option. Evidence labels do not verify backups. No automatic down migrations.")
			f.SetOutput(out)
			f.PrintDefaults()
			return 0
		}
		fmt.Fprintln(stderr, "invalid migration arguments (values suppressed); use --help")
		return 1
	}
	if f.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments (values suppressed)")
		return 1
	}
	result, err := migrations.Run(ctx, getenv("DATABASE_URL"), o)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if o.Mode == "plan" {
		for _, p := range result.Pending {
			fmt.Fprintf(out, "pending %s sha256=%s\n", p.Name, p.SHA256)
		}
		fmt.Fprintf(out, "plan: %d pending; no migrations or ledger DDL executed\n", len(result.Pending))
	} else {
		fmt.Fprintf(out, "apply: %d migrations committed; evidence labels recorded, not verified\n", result.Applied)
	}
	return 0
}
