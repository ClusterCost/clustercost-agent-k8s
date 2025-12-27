package forwarder

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Sender posts AgentReports to a remote endpoint.
type Sender struct {
	client    *http.Client
	endpoint  string
	authToken string
	gzip      bool
}

// NewSender returns a configured Sender.
func NewSender(endpoint, authToken string, timeout time.Duration, gzipEnabled bool) *Sender {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Sender{
		client: &http.Client{
			Timeout: timeout,
		},
		endpoint:  endpoint,
		authToken: authToken,
		gzip:      gzipEnabled,
	}
}

// Send POSTs the report to the remote endpoint.
func (s *Sender) Send(ctx context.Context, report AgentReport) error {
	if s == nil || s.endpoint == "" {
		return nil
	}
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	payload, err := s.wrap(body)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, payload)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.gzip {
		req.Header.Set("Content-Encoding", "gzip")
	}
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send report: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("remote endpoint returned status %d", resp.StatusCode)
	}
	return nil
}

// SendBatch POSTs a list of reports to the remote endpoint.
func (s *Sender) SendBatch(ctx context.Context, reports []AgentReport) error {
	if s == nil || s.endpoint == "" {
		return nil
	}
	if len(reports) == 0 {
		return nil
	}
	payload := map[string]any{"reports": reports}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal batch: %w", err)
	}
	payloadReader, err := s.wrap(body)
	if err != nil {
		return fmt.Errorf("encode batch: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, payloadReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.gzip {
		req.Header.Set("Content-Encoding", "gzip")
	}
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send batch: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("remote endpoint returned status %d", resp.StatusCode)
	}
	return nil
}

func (s *Sender) wrap(payload []byte) (*bytes.Reader, error) {
	if !s.gzip {
		return bytes.NewReader(payload), nil
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return bytes.NewReader(buf.Bytes()), nil
}
