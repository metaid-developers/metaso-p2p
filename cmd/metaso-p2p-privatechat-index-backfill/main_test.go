package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseOptions(t *testing.T) {
	opts, err := parseOptions([]string{"--data-dir", "/tmp/metaso-data", "--timeout", "5m", "--verify-only"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.dataDir != "/tmp/metaso-data" || opts.timeout != 5*time.Minute || !opts.verifyOnly {
		t.Fatalf("unexpected options: %+v", opts)
	}
}

func TestParseOptionsRequiresDataDir(t *testing.T) {
	if _, err := parseOptions(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("expected missing data dir error")
	}
}

func TestRunBackfillAndVerifyEmptyDatabase(t *testing.T) {
	opts := options{dataDir: t.TempDir(), timeout: time.Minute}
	var output bytes.Buffer
	if err := run(context.Background(), opts, &output); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !strings.Contains(output.String(), `"status": "ready"`) {
		t.Fatalf("backfill output=%s", output.String())
	}

	output.Reset()
	opts.verifyOnly = true
	if err := run(context.Background(), opts, &output); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(output.String(), `"indexedCount": 0`) {
		t.Fatalf("verify output=%s", output.String())
	}
}
