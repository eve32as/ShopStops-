package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// Help and invalid flags must never echo submitted secrets.
func TestCLIHelpAndSecretSuppression(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--mode=down"}, {"--timeout=driver-secret"}, {"--unknown=driver-secret"}, {"driver-secret"}, {"--mode=apply"}} {
		var out, stderr bytes.Buffer
		code := run(context.Background(), args, func(string) string { return "postgres://driver-secret@invalid" }, &out, &stderr)
		if strings.Contains(out.String()+stderr.String(), "driver-secret") {
			t.Fatal("CLI leaked input")
		}
		if args[0] == "--help" {
			if code != 0 || !strings.Contains(out.String(), "read-only default") || !strings.Contains(out.String(), "no baseline/stamp") {
				t.Fatal("missing safety help")
			}
		} else if code == 0 {
			t.Fatal("invalid arguments accepted")
		}
	}
}

func TestCLIInvalidDSNDefaultPlan(t *testing.T) {
	var out, stderr bytes.Buffer
	code := run(context.Background(), []string{"--migrations-dir=../../db/migrations"}, func(string) string { return "postgres://driver-secret@invalid?sslmode=nonsense" }, &out, &stderr)
	if code != 1 || strings.Contains(stderr.String(), "driver-secret") || !strings.Contains(stderr.String(), "configuration") {
		t.Fatal("invalid DSN not safely rejected")
	}
}
