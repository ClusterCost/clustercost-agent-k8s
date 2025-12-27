package forwarder

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"clustercost-agent-k8s/internal/snapshot"
)

func TestSenderSend(t *testing.T) {
	var got AgentReport
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer token-123" {
			t.Fatalf("missing auth header")
		}
		body := readBody(t, r)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	sender := NewSender(server.URL, "token-123", 2*time.Second, false)
	report := AgentReport{
		ClusterID:   "cluster-1",
		ClusterName: "prod",
		NodeName:    "node-a",
		Version:     "v0.1.0",
		Timestamp:   time.Unix(123, 0).UTC(),
		Snapshot:    snapshot.Snapshot{},
	}

	if err := sender.Send(context.Background(), report); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.ClusterID != "cluster-1" || got.NodeName != "node-a" {
		t.Fatalf("unexpected report payload: %+v", got)
	}
}

func TestSenderSendBatch(t *testing.T) {
	var payload map[string][]AgentReport
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("expected gzip content encoding")
		}
		body := readBody(t, r)
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := NewSender(server.URL, "", 2*time.Second, true)
	reports := []AgentReport{
		{ClusterID: "cluster-1", NodeName: "node-a"},
		{ClusterID: "cluster-1", NodeName: "node-b"},
	}
	if err := sender.SendBatch(context.Background(), reports); err != nil {
		t.Fatalf("send batch: %v", err)
	}
	if len(payload["reports"]) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(payload["reports"]))
	}
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	var reader io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		defer zr.Close()
		reader = zr
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}
