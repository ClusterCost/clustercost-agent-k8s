package forwarder

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRetriesFromName(t *testing.T) {
	name := "123_000001_r3.json"
	if got := parseRetries(name); got != 3 {
		t.Fatalf("expected 3 retries, got %d", got)
	}
}

func TestQueueMemorySpill(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dir := t.TempDir()
	sender := NewSender(server.URL, "", 2*time.Second, false)
	queue := NewQueue(dir, 50, 3, time.Second, time.Second, 1024, 1, sender, noopLogger())

	if err := queue.Enqueue(AgentReport{ClusterID: "c1"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := queue.Enqueue(AgentReport{ClusterID: "c2"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	found := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) == ".json" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected spill file in queue dir")
	}
}

func TestBackoffDuration(t *testing.T) {
	base := 2 * time.Second
	queue := NewQueue(t.TempDir(), 10, 3, base, time.Second, 1000, 1, nil, noopLogger())
	got := queue.backoffDuration(0)
	if got < time.Duration(float64(base)*0.8) || got > time.Duration(float64(base)*1.2) {
		t.Fatalf("expected jittered base backoff, got %s", got)
	}
	got = queue.backoffDuration(2)
	expected := 8 * time.Second
	if got < time.Duration(float64(expected)*0.8) || got > time.Duration(float64(expected)*1.2) {
		t.Fatalf("expected jittered exponential backoff, got %s", got)
	}
}

func TestQueueFlushMemorySendsBatch(t *testing.T) {
	var gotCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string][]AgentReport
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		gotCount = len(payload["reports"])
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := NewSender(server.URL, "", 2*time.Second, false)
	queue := NewQueue(t.TempDir(), 10, 3, time.Second, time.Second, 10*1024, 10, sender, noopLogger())

	reportA := AgentReport{ClusterID: "a"}
	reportB := AgentReport{ClusterID: "b"}
	if err := queue.Enqueue(reportA); err != nil {
		t.Fatalf("enqueue a: %v", err)
	}
	if err := queue.Enqueue(reportB); err != nil {
		t.Fatalf("enqueue b: %v", err)
	}

	queue.flushMemory(context.Background())
	if gotCount != 2 {
		t.Fatalf("expected 2 reports in batch, got %d", gotCount)
	}
}

func TestQueueDiskBatchRespectsBatchBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dir := t.TempDir()
	sender := NewSender(server.URL, "", 2*time.Second, false)
	queue := NewQueue(dir, 10, 3, time.Millisecond, time.Second, 120, 0, sender, noopLogger())

	report := AgentReport{ClusterID: strings.Repeat("x", 80)}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1_r0.json"), data, 0o644); err != nil {
		t.Fatalf("write file 1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2_r0.json"), data, 0o644); err != nil {
		t.Fatalf("write file 2: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "1_r0.json"), old, old)
	_ = os.Chtimes(filepath.Join(dir, "2_r0.json"), old, old)

	queue.flushOnce(context.Background())

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var remaining int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) == ".json" {
			remaining++
		}
	}
	if remaining != 1 {
		t.Fatalf("expected 1 file remaining, got %d", remaining)
	}
}

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
