package collector

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"time"
	"unsafe"

	"clustercost-agent-k8s/internal/config"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/ringbuf"
)

const (
	dnsFamilyIPv4   = 2
	dnsFamilyIPv6   = 10
	dnsMaxNameBytes = 128
	dnsEventSize    = int(unsafe.Sizeof(dnsEvent{}))
)

// DNSCache maintains a best-effort IP->DNS mapping from observed DNS responses.
type DNSCache struct {
	mu         sync.RWMutex
	entries    map[netip.Addr]dnsEntry
	maxEntries int
	defaultTTL time.Duration
	reader     *ringbuf.Reader
	logger     *slog.Logger
}

type dnsEntry struct {
	name    string
	expires time.Time
}

type dnsEvent struct {
	Family  uint8
	NameLen uint8
	_       [2]byte
	TTL     uint32
	Addr    [16]byte
	Name    [dnsMaxNameBytes]byte
}

// NewDNSCache opens the DNS ring buffer map and returns a cache.
func NewDNSCache(cfg config.NetworkConfig, logger *slog.Logger) *DNSCache {
	if !cfg.DNSCapture || cfg.DNSMapPath == "" {
		return nil
	}
	mp, err := ebpf.LoadPinnedMap(cfg.DNSMapPath, nil)
	if err != nil {
		if logger != nil {
			logger.Warn("dns map not available; dns capture disabled", slog.String("error", err.Error()))
		}
		return nil
	}
	reader, err := ringbuf.NewReader(mp)
	if err != nil {
		if logger != nil {
			logger.Warn("dns ringbuf reader failed", slog.String("error", err.Error()))
		}
		_ = mp.Close()
		return nil
	}

	maxEntries := cfg.DNSCacheEntries
	if maxEntries <= 0 {
		maxEntries = 10000
	}

	return &DNSCache{
		entries:    make(map[netip.Addr]dnsEntry, maxEntries),
		maxEntries: maxEntries,
		defaultTTL: 60 * time.Second,
		reader:     reader,
		logger:     logger,
	}
}

// Run consumes DNS events until the context is cancelled.
func (c *DNSCache) Run(ctx context.Context) {
	if c == nil || c.reader == nil {
		return
	}
	go func() {
		<-ctx.Done()
		_ = c.reader.Close()
	}()
	for {
		record, err := c.reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) || errors.Is(err, context.Canceled) {
				return
			}
			if c.logger != nil {
				c.logger.Debug("dns ringbuf read failed", slog.String("error", err.Error()))
			}
			continue
		}
		c.ingest(record.RawSample)
	}
}

// Snapshot returns a copy of the current cache with expired entries removed.
func (c *DNSCache) Snapshot() map[netip.Addr]string {
	if c == nil {
		return map[netip.Addr]string{}
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	result := make(map[netip.Addr]string, len(c.entries))
	for ip, entry := range c.entries {
		if entry.expires.Before(now) {
			delete(c.entries, ip)
			continue
		}
		result[ip] = entry.name
	}
	return result
}

func (c *DNSCache) ingest(payload []byte) {
	if len(payload) < dnsEventSize {
		return
	}
	var event dnsEvent
	copy((*[dnsEventSize]byte)(unsafe.Pointer(&event))[:], payload) // #nosec G103

	nameLen := int(event.NameLen)
	if nameLen <= 0 || nameLen >= dnsMaxNameBytes {
		return
	}
	name, ok := decodeQName(event.Name[:nameLen])
	if !ok || name == "" {
		return
	}
	ip, ok := dnsEventIP(event)
	if !ok {
		return
	}
	ttlSeconds := event.TTL
	if ttlSeconds == 0 {
		ttlSeconds = uint32(c.defaultTTL.Seconds())
	}
	expires := time.Now().Add(time.Duration(ttlSeconds) * time.Second)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[ip] = dnsEntry{name: name, expires: expires}
	if len(c.entries) > c.maxEntries {
		c.evict(time.Now())
	}
}

func (c *DNSCache) evict(now time.Time) {
	for ip, entry := range c.entries {
		if entry.expires.Before(now) {
			delete(c.entries, ip)
		}
	}
	for len(c.entries) > c.maxEntries {
		for ip := range c.entries {
			delete(c.entries, ip)
			break
		}
	}
}

func dnsEventIP(event dnsEvent) (netip.Addr, bool) {
	switch event.Family {
	case dnsFamilyIPv4:
		var addr [4]byte
		copy(addr[:], event.Addr[:4])
		return netip.AddrFrom4(addr), true
	case dnsFamilyIPv6:
		return netip.AddrFrom16(event.Addr), true
	default:
		return netip.Addr{}, false
	}
}

func decodeQName(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		labelLen := int(raw[i])
		if labelLen == 0 {
			break
		}
		if labelLen&0xC0 != 0 {
			return "", false
		}
		i++
		if labelLen > 63 || i+labelLen > len(raw) {
			return "", false
		}
		if len(out) > 0 {
			out = append(out, '.')
		}
		out = append(out, raw[i:i+labelLen]...)
		i += labelLen
	}
	if len(out) == 0 {
		return "", false
	}
	return string(out), true
}
