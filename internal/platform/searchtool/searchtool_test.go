package searchtool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newWS(t *testing.T) *Tool {
	t.Helper()
	return New(t.TempDir())
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSearch_PathEscapeRejected(t *testing.T) {
	tool := newWS(t)
	parent := filepath.Dir(tool.root)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "x", "path": "../evil"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("escape must be IsError")
	}
	res, err = tool.Execute(context.Background(), args(t, map[string]any{"pattern": "x", "path": parent}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("absolute escape must be IsError")
	}
}

func TestSearch_OutputBounded(t *testing.T) {
	tool := newWS(t)
	for i := 0; i < 80; i++ {
		write(t, tool.root, filepath.Join("src", fmt.Sprintf("%03d.go", i)), "needle here")
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle", "max_matches": 5}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	body := strings.TrimSuffix(res.Content, "\n")
	lines := strings.Split(body, "\n")
	matchLines := 0
	for _, ln := range lines {
		if strings.Contains(ln, "needle") && !strings.Contains(ln, "truncated") {
			matchLines++
		}
	}
	if matchLines != 5 {
		t.Fatalf("match lines = %d, want 5; %s", matchLines, res.Content)
	}
	if !strings.Contains(res.Content, "truncated") {
		t.Fatal("must annotate max_matches truncation")
	}
}

func TestSearch_OutputByteBounded(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "big.txt", strings.Repeat("x", 64*1024+1024)+"needle")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("byte-limit truncation must not be IsError: %+v", res)
	}
	if strings.TrimSpace(res.Content) == "no matches" || strings.Contains(res.Content, "no matches") {
		t.Fatalf("oversized first hit must not become no matches: %q", res.Content)
	}
	if !strings.Contains(res.Content, "truncated") || !strings.Contains(res.Content, "64KiB") {
		t.Fatalf("must annotate 64KiB truncation: %s", res.Content)
	}
}

func TestSearch_MaxMatchesExactNoTruncate(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "a.txt", "needle\nneedle")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle", "max_matches": 2}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	if strings.Contains(res.Content, "truncated") {
		t.Fatalf("exact max_matches with nothing left must not annotate: %s", res.Content)
	}
	body := strings.TrimSuffix(res.Content, "\n")
	matchLines := 0
	for _, ln := range strings.Split(body, "\n") {
		if strings.Contains(ln, "needle") {
			matchLines++
		}
	}
	if matchLines != 2 {
		t.Fatalf("match lines = %d, want 2; %s", matchLines, res.Content)
	}
}

func TestSearch_InvalidPattern(t *testing.T) {
	tool := newWS(t)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "["}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "invalid pattern") {
		t.Fatalf("%+v", res)
	}
}

func TestSearch_SkipsIgnoredAndBinary(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "lib/a.go", "HIT-lib")
	write(t, tool.root, "vendor/a.go", "HIT-vendor")
	write(t, tool.root, "bin/a.go", "HIT-bin")
	if err := os.WriteFile(filepath.Join(tool.root, "lib", "blob.bin"), []byte("HIT-\x00binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "lib/a.go") && !strings.Contains(res.Content, `lib\a.go`) {
		t.Fatalf("missing lib hit: %s", res.Content)
	}
	if strings.Contains(res.Content, "vendor") || strings.Contains(res.Content, "HIT-bin") || strings.Contains(res.Content, "blob.bin") {
		t.Fatalf("ignored/binary leaked: %s", res.Content)
	}

	res, err = tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-", "path": "vendor"}))
	if err != nil || res.IsError {
		t.Fatalf("explicit vendor: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "HIT-vendor") {
		t.Fatalf("explicit vendor root must be searched: %s", res.Content)
	}
}

func TestSearch_NoMatchIsSuccess(t *testing.T) {
	tool := newWS(t)
	write(t, tool.root, "a.txt", "hello")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "zzz-nope"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(res.Content, "no matches") {
		t.Fatalf("%+v", res)
	}
}

func TestSearch_TimeoutPartial(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	for i := 0; i < 40000; i++ {
		body.WriteString("needle line\n")
	}
	write(t, root, "big.txt", body.String())
	tool := NewWithTimeout(root, time.Nanosecond)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "needle", "max_matches": 200}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("want TimedOut, got %+v", res)
	}
	if res.Content == "" {
		t.Fatal("Content must be non-empty")
	}
	hasHit := strings.Contains(res.Content, "big.txt:")
	hasTimeout := strings.Contains(res.Content, "TIMEOUT")
	if !hasHit && !hasTimeout {
		t.Fatalf("want path:line: partial or TIMEOUT, got %q", res.Content)
	}
}
