package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Queue handles local spooling and batched retries.
type Queue struct {
	dir           string
	failDir       string
	maxBatch      int
	maxRetries    int
	backoff       time.Duration
	flushEvery    time.Duration
	maxBatchBytes int64
	memoryBuffer  int
	sender        *Sender
	logger        *slog.Logger
	mu            sync.Mutex
	mem           []queuedReport
	memBytes      int64
	flushCh       chan struct{}
	randMu        sync.Mutex
	rng           *rand.Rand
}

// NewQueue returns a configured Queue.
func NewQueue(dir string, maxBatch, maxRetries int, backoff, flushEvery time.Duration, maxBatchBytes int64, memoryBuffer int, sender *Sender, logger *slog.Logger) *Queue {
	if maxBatch <= 0 {
		maxBatch = 50
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	if backoff <= 0 {
		backoff = 10 * time.Second
	}
	if flushEvery <= 0 {
		flushEvery = 5 * time.Second
	}
	if maxBatchBytes <= 0 {
		maxBatchBytes = 512 * 1024
	}
	if memoryBuffer < 0 {
		memoryBuffer = 0
	}
	return &Queue{
		dir:           dir,
		failDir:       filepath.Join(dir, "failed"),
		maxBatch:      maxBatch,
		maxRetries:    maxRetries,
		backoff:       backoff,
		flushEvery:    flushEvery,
		maxBatchBytes: maxBatchBytes,
		memoryBuffer:  memoryBuffer,
		sender:        sender,
		logger:        logger,
		flushCh:       make(chan struct{}, 1),
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Enqueue writes a report to the queue directory.
func (q *Queue) Enqueue(report AgentReport) error {
	if q == nil || q.sender == nil || q.dir == "" {
		return nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	if q.memoryBuffer == 0 {
		return q.writeToDisk(data)
	}

	shouldFlush := false
	q.mu.Lock()
	q.mem = append(q.mem, queuedReport{report: report, raw: data, size: int64(len(data))})
	q.memBytes += int64(len(data))
	if q.memBytes >= q.maxBatchBytes && q.maxBatchBytes > 0 {
		shouldFlush = true
	}
	for q.memoryBuffer > 0 && (len(q.mem) > q.memoryBuffer || q.memBytes > q.maxBatchBytes) {
		item := q.mem[0]
		q.mem = q.mem[1:]
		q.memBytes -= item.size
		if err := q.writeToDisk(item.raw); err != nil {
			q.logger.Warn("spill to disk failed", slog.String("error", err.Error()))
		}
	}
	q.mu.Unlock()

	if shouldFlush {
		q.signalFlush()
	}
	return nil
}

// Run flushes queued reports on an interval until ctx is done.
func (q *Queue) Run(ctx context.Context) {
	if q == nil || q.sender == nil || q.dir == "" {
		return
	}
	ticker := time.NewTicker(q.flushEvery)
	defer ticker.Stop()

	for {
		q.flushOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-q.flushCh:
			q.flushOnce(ctx)
		}
	}
}

func (q *Queue) flushOnce(ctx context.Context) {
	if err := os.MkdirAll(q.dir, 0o755); err != nil {
		q.logger.Warn("create queue dir failed", slog.String("error", err.Error()))
		return
	}

	if q.flushMemory(ctx) {
		return
	}

	entries, err := os.ReadDir(q.dir)
	if err != nil {
		q.logger.Warn("read queue dir failed", slog.String("error", err.Error()))
		return
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".json") {
			files = append(files, name)
		}
	}
	sort.Strings(files)

	batchFiles := make([]string, 0, q.maxBatch)
	var batchBytes int64
	now := time.Now()
	for _, name := range files {
		if len(batchFiles) >= q.maxBatch {
			break
		}
		path := filepath.Join(q.dir, name)
		retries := parseRetries(name)
		if retries > q.maxRetries {
			q.moveToFailed(path)
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) < q.backoffDuration(retries) {
			continue
		}
		if batchBytes+info.Size() > q.maxBatchBytes && len(batchFiles) > 0 {
			break
		}
		batchFiles = append(batchFiles, path)
		batchBytes += info.Size()
	}

	if len(batchFiles) == 0 {
		return
	}

	reports := make([]AgentReport, 0, len(batchFiles))
	for _, path := range batchFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			q.logger.Warn("read queue file failed", slog.String("error", err.Error()))
			continue
		}
		var report AgentReport
		if err := json.Unmarshal(data, &report); err != nil {
			q.logger.Warn("decode queue file failed", slog.String("error", err.Error()))
			continue
		}
		reports = append(reports, report)
	}

	if len(reports) == 0 {
		return
	}

	if err := q.sender.SendBatch(ctx, reports); err != nil {
		q.logger.Warn("remote batch send failed", slog.String("error", err.Error()))
		q.bumpRetries(batchFiles)
		return
	}

	for _, path := range batchFiles {
		if err := os.Remove(path); err != nil {
			q.logger.Warn("remove queue file failed", slog.String("error", err.Error()))
		}
	}
}

func (q *Queue) bumpRetries(paths []string) {
	for _, path := range paths {
		base := filepath.Base(path)
		retries := parseRetries(base) + 1
		next := setRetries(base, retries)
		dst := filepath.Join(q.dir, next)
		_ = os.Chtimes(path, time.Now(), time.Now())
		if err := os.Rename(path, dst); err != nil {
			q.logger.Warn("rename queue file failed", slog.String("error", err.Error()))
		}
	}
}

func (q *Queue) moveToFailed(path string) {
	if err := os.MkdirAll(q.failDir, 0o755); err != nil {
		return
	}
	dst := filepath.Join(q.failDir, filepath.Base(path))
	_ = os.Rename(path, dst)
}

func parseRetries(name string) int {
	idx := strings.LastIndex(name, "_r")
	if idx == -1 {
		return 0
	}
	part := strings.TrimSuffix(name[idx+2:], ".json")
	retries, err := strconv.Atoi(part)
	if err != nil {
		return 0
	}
	return retries
}

func setRetries(name string, retries int) string {
	idx := strings.LastIndex(name, "_r")
	if idx == -1 {
		return name
	}
	base := name[:idx+2]
	return base + strconv.Itoa(retries) + ".json"
}

// retriesFromName is kept for tests.
type queuedReport struct {
	report AgentReport
	raw    []byte
	size   int64
}

func (q *Queue) flushMemory(ctx context.Context) bool {
	q.mu.Lock()
	if len(q.mem) == 0 {
		q.mu.Unlock()
		return false
	}
	batch := make([]queuedReport, 0, q.maxBatch)
	var batchBytes int64
	for _, item := range q.mem {
		if len(batch) >= q.maxBatch {
			break
		}
		if batchBytes+item.size > q.maxBatchBytes && len(batch) > 0 {
			break
		}
		batch = append(batch, item)
		batchBytes += item.size
	}
	q.mem = q.mem[len(batch):]
	q.memBytes -= batchBytes
	q.mu.Unlock()

	reports := make([]AgentReport, 0, len(batch))
	for _, item := range batch {
		reports = append(reports, item.report)
	}
	if err := q.sender.SendBatch(ctx, reports); err != nil {
		q.logger.Warn("remote in-memory send failed", slog.String("error", err.Error()))
		for _, item := range batch {
			if err := q.writeToDisk(item.raw); err != nil {
				q.logger.Warn("spill to disk failed", slog.String("error", err.Error()))
			}
		}
	}
	return true
}

func (q *Queue) writeToDisk(data []byte) error {
	if err := os.MkdirAll(q.dir, 0o755); err != nil {
		return fmt.Errorf("create queue dir: %w", err)
	}
	name := fmt.Sprintf("%d_%06d_r0.json", time.Now().UTC().UnixNano(), q.randIntn(1_000_000))
	tmpPath := filepath.Join(q.dir, name+".tmp")
	finalPath := filepath.Join(q.dir, name)
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write queue file: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("commit queue file: %w", err)
	}
	return nil
}

func (q *Queue) backoffDuration(retries int) time.Duration {
	base := q.backoff
	if base <= 0 {
		base = 10 * time.Second
	}
	if retries <= 0 {
		return base
	}
	if retries > 12 {
		retries = 12
	}
	backoff := base * time.Duration(1<<retries)
	jitter := 0.2
	factor := 1 - jitter + q.randFloat64()*(2*jitter)
	return time.Duration(float64(backoff) * factor)
}

func (q *Queue) signalFlush() {
	select {
	case q.flushCh <- struct{}{}:
	default:
	}
}

func (q *Queue) randFloat64() float64 {
	q.randMu.Lock()
	defer q.randMu.Unlock()
	return q.rng.Float64()
}

func (q *Queue) randIntn(n int) int {
	q.randMu.Lock()
	defer q.randMu.Unlock()
	return q.rng.Intn(n)
}
