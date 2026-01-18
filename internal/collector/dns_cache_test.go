package collector

import (
	"net/netip"
	"testing"
	"time"
	"unsafe"
)

func TestDNSCacheIngestAndSnapshot(t *testing.T) {
	cache := &DNSCache{
		entries:    map[netip.Addr]dnsEntry{},
		maxEntries: 10,
		defaultTTL: 60 * time.Second,
	}

	ip := netip.MustParseAddr("1.2.3.4")
	cache.ingest(encodeDNSEvent(ip, "example.com", 30))

	snap := cache.Snapshot()
	if got := snap[ip]; got != "example.com" {
		t.Fatalf("expected dns name example.com, got %q", got)
	}
}

func TestDNSCacheExpiresEntries(t *testing.T) {
	ip := netip.MustParseAddr("10.0.0.1")
	cache := &DNSCache{
		entries: map[netip.Addr]dnsEntry{
			ip: {name: "stale.local", expires: time.Now().Add(-1 * time.Minute)},
		},
		maxEntries: 10,
		defaultTTL: 60 * time.Second,
	}

	snap := cache.Snapshot()
	if _, ok := snap[ip]; ok {
		t.Fatalf("expected expired entry to be removed")
	}
	if len(cache.entries) != 0 {
		t.Fatalf("expected cache to be pruned")
	}
}

func TestDNSCacheEvictsWhenFull(t *testing.T) {
	cache := &DNSCache{
		entries:    map[netip.Addr]dnsEntry{},
		maxEntries: 1,
		defaultTTL: 60 * time.Second,
	}

	cache.ingest(encodeDNSEvent(netip.MustParseAddr("1.1.1.1"), "one.local", 30))
	cache.ingest(encodeDNSEvent(netip.MustParseAddr("2.2.2.2"), "two.local", 30))

	if got := len(cache.entries); got > cache.maxEntries {
		t.Fatalf("expected cache size <= %d, got %d", cache.maxEntries, got)
	}
}

func encodeDNSEvent(ip netip.Addr, name string, ttl uint32) []byte {
	var event dnsEvent
	event.TTL = ttl

	raw := encodeQName(name)
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > dnsMaxNameBytes {
		raw = raw[:dnsMaxNameBytes]
	}
	event.NameLen = uint8(len(raw))
	copy(event.Name[:], raw)
	if ip.Is6() {
		event.Family = dnsFamilyIPv6
		addr := ip.As16()
		copy(event.Addr[:], addr[:])
	} else {
		event.Family = dnsFamilyIPv4
		addr := ip.As4()
		copy(event.Addr[:4], addr[:])
	}

	size := int(unsafe.Sizeof(dnsEvent{}))
	buf := make([]byte, size)
	copy(buf, (*[1 << 20]byte)(unsafe.Pointer(&event))[:size])
	return buf
}

func encodeQName(name string) []byte {
	if name == "" {
		return nil
	}
	out := make([]byte, 0, dnsMaxNameBytes)
	for _, part := range splitDNSName(name) {
		if len(part) == 0 || len(part) > 63 {
			return nil
		}
		out = append(out, byte(len(part)))
		out = append(out, []byte(part)...)
	}
	out = append(out, 0)
	return out
}

func splitDNSName(name string) []string {
	parts := []string{}
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			parts = append(parts, name[start:i])
			start = i + 1
		}
	}
	return parts
}
