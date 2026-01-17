// SPDX-License-Identifier: GPL-2.0

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

#ifndef BPF_F_NO_PREALLOC
#define BPF_F_NO_PREALLOC 1
#endif

struct flow_key {
	__u8 src_addr[16];
	__u8 dst_addr[16];
	__u8 family;
	__u8 proto;
	__u8 pad[2];
};

struct flow_counters {
	__u64 tx_bytes;
	__u64 rx_bytes;
};

struct dns_hdr {
	__u16 id;
	__u16 flags;
	__u16 qdcount;
	__u16 ancount;
	__u16 nscount;
	__u16 arcount;
};

struct dns_answer_fixed {
	__u16 type;
	__u16 class;
	__u32 ttl;
	__u16 rdlen;
};

struct dns_event {
	__u8 family;
	__u8 name_len;
	__u16 pad;
	__u32 ttl;
	__u8 addr[16];
	char name[128];
};

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 16384);
	__type(key, struct flow_key);
	__type(value, struct flow_counters);
} clustercost_flows SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 1 << 20);
} clustercost_dns_events SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, __u32);
} clustercost_dns_config SEC(".maps");

#define DNS_MAX_NAME 128
#define DNS_MAX_LABELS 16
#define DNS_PORT 53
#define IPPROTO_UDP 17
#define IPPROTO_TCP 6

static __always_inline void fill_ipv4(__u8 dst[16], __u32 addr) {
	*(__u32 *)dst = addr;
}

static __always_inline void fill_ipv6(__u8 dst[16], const __u8 *src) {
	#pragma unroll
	for (int i = 0; i < 16; i++) {
		dst[i] = src[i];
	}
}

static __always_inline int read_qname_raw(struct __sk_buff *skb, __u32 *cursor, char *out, __u8 *out_len) {
	#pragma unroll
	for (int i = 0; i < DNS_MAX_NAME; i++) {
		__u8 b = 0;
		if (bpf_skb_load_bytes(skb, *cursor + i, &b, sizeof(b)) < 0) {
			return 0;
		}
		if (b & 0xC0) {
			return 0;
		}
		out[i] = b;
		if (b == 0) {
			*out_len = i + 1;
			*cursor += i + 1;
			return 1;
		}
	}
	return 0;
}

static __always_inline int skip_name(struct __sk_buff *skb, __u32 *cursor) {
	#pragma unroll
	for (int i = 0; i < DNS_MAX_LABELS; i++) {
		__u8 len = 0;
		if (bpf_skb_load_bytes(skb, *cursor, &len, sizeof(len)) < 0) {
			return 0;
		}
		if (len == 0) {
			*cursor += 1;
			return 1;
		}
		if (len & 0xC0) {
			*cursor += 2;
			return 1;
		}
		*cursor += 1 + len;
	}
	return 0;
}

static __always_inline void maybe_emit_dns(struct __sk_buff *skb, __u32 dns_offset, __u8 family) {
	__u32 key = 0;
	__u32 *sample = bpf_map_lookup_elem(&clustercost_dns_config, &key);
	if (sample && *sample < 100) {
		__u32 rand = bpf_get_prandom_u32();
		if ((rand % 100) >= *sample) {
			return;
		}
	}

	struct dns_hdr hdr = {};
	if (bpf_skb_load_bytes(skb, dns_offset, &hdr, sizeof(hdr)) < 0) {
		return;
	}
	if (!(bpf_ntohs(hdr.flags) & 0x8000)) {
		return;
	}
	if (bpf_ntohs(hdr.qdcount) == 0 || bpf_ntohs(hdr.ancount) == 0) {
		return;
	}
	__u32 cursor = dns_offset + sizeof(hdr);
	struct dns_event *event = bpf_ringbuf_reserve(&clustercost_dns_events, sizeof(*event), 0);
	if (!event) {
		return;
	}
	__u8 name_len = 0;
	if (!read_qname_raw(skb, &cursor, event->name, &name_len)) {
		bpf_ringbuf_discard(event, 0);
		return;
	}
	cursor += 4; // qtype + qclass
	if (!skip_name(skb, &cursor)) {
		bpf_ringbuf_discard(event, 0);
		return;
	}

	struct dns_answer_fixed ans = {};
	if (bpf_skb_load_bytes(skb, cursor, &ans, sizeof(ans)) < 0) {
		bpf_ringbuf_discard(event, 0);
		return;
	}
	cursor += sizeof(ans);
	__u16 atype = bpf_ntohs(ans.type);
	__u16 rdlen = bpf_ntohs(ans.rdlen);
	__u32 ttl = bpf_ntohl(ans.ttl);
	event->family = family;
	event->name_len = name_len;
	event->ttl = ttl;
	__builtin_memset(event->addr, 0, sizeof(event->addr));

	if (atype == 1 && rdlen == 4) {
		if (bpf_skb_load_bytes(skb, cursor, event->addr, 4) < 0) {
			bpf_ringbuf_discard(event, 0);
			return;
		}
		bpf_ringbuf_submit(event, 0);
		return;
	}
	if (atype == 28 && rdlen == 16) {
		if (bpf_skb_load_bytes(skb, cursor, event->addr, 16) < 0) {
			bpf_ringbuf_discard(event, 0);
			return;
		}
		bpf_ringbuf_submit(event, 0);
		return;
	}

	bpf_ringbuf_discard(event, 0);
}

static __always_inline void maybe_emit_dns_tcp(struct __sk_buff *skb, __u32 tcp_offset, __u8 family) {
	struct tcphdr tcp = {};
	if (bpf_skb_load_bytes(skb, tcp_offset, &tcp, sizeof(tcp)) < 0) {
		return;
	}
	__u32 tcp_len = tcp.doff * 4;
	if (tcp_len < sizeof(tcp)) {
		return;
	}
	__u32 dns_offset = tcp_offset + tcp_len;
	__u16 dns_len = 0;
	if (bpf_skb_load_bytes(skb, dns_offset, &dns_len, sizeof(dns_len)) < 0) {
		return;
	}
	dns_offset += sizeof(dns_len);
	maybe_emit_dns(skb, dns_offset, family);
}

static __always_inline int handle_skb(struct __sk_buff *skb, bool egress) {
	struct flow_key key = {};
	struct flow_counters *stats;

	__u16 proto = bpf_ntohs(skb->protocol);
	if (proto == 0x0800) {
		struct iphdr iph;
		if (bpf_skb_load_bytes(skb, 0, &iph, sizeof(iph)) < 0) {
			return 1;
		}
		key.family = 2; // AF_INET
		key.proto = iph.protocol;
		fill_ipv4(key.src_addr, iph.saddr);
		fill_ipv4(key.dst_addr, iph.daddr);
		if (iph.protocol == IPPROTO_UDP) {
			__u32 ihl = iph.ihl * 4;
			struct udphdr udp = {};
			if (bpf_skb_load_bytes(skb, ihl, &udp, sizeof(udp)) >= 0) {
				__u16 sport = bpf_ntohs(udp.source);
				__u16 dport = bpf_ntohs(udp.dest);
				if (sport == DNS_PORT || dport == DNS_PORT) {
					maybe_emit_dns(skb, ihl + sizeof(udp), key.family);
				}
			}
		} else if (iph.protocol == IPPROTO_TCP) {
			__u32 ihl = iph.ihl * 4;
			struct tcphdr tcp = {};
			if (bpf_skb_load_bytes(skb, ihl, &tcp, sizeof(tcp)) >= 0) {
				__u16 sport = bpf_ntohs(tcp.source);
				__u16 dport = bpf_ntohs(tcp.dest);
				if (sport == DNS_PORT || dport == DNS_PORT) {
					maybe_emit_dns_tcp(skb, ihl, key.family);
				}
			}
		}
	} else if (proto == 0x86DD) {
		struct ipv6hdr iph6;
		if (bpf_skb_load_bytes(skb, 0, &iph6, sizeof(iph6)) < 0) {
			return 1;
		}
		key.family = 10; // AF_INET6
		key.proto = iph6.nexthdr;
		fill_ipv6(key.src_addr, iph6.saddr.in6_u.u6_addr8);
		fill_ipv6(key.dst_addr, iph6.daddr.in6_u.u6_addr8);
		if (iph6.nexthdr == IPPROTO_UDP) {
			__u32 ihl6 = sizeof(struct ipv6hdr);
			struct udphdr udp = {};
			if (bpf_skb_load_bytes(skb, ihl6, &udp, sizeof(udp)) >= 0) {
				__u16 sport = bpf_ntohs(udp.source);
				__u16 dport = bpf_ntohs(udp.dest);
				if (sport == DNS_PORT || dport == DNS_PORT) {
					maybe_emit_dns(skb, ihl6 + sizeof(udp), key.family);
				}
			}
		} else if (iph6.nexthdr == IPPROTO_TCP) {
			__u32 ihl6 = sizeof(struct ipv6hdr);
			struct tcphdr tcp = {};
			if (bpf_skb_load_bytes(skb, ihl6, &tcp, sizeof(tcp)) >= 0) {
				__u16 sport = bpf_ntohs(tcp.source);
				__u16 dport = bpf_ntohs(tcp.dest);
				if (sport == DNS_PORT || dport == DNS_PORT) {
					maybe_emit_dns_tcp(skb, ihl6, key.family);
				}
			}
		}
	} else {
		return 1;
	}

	stats = bpf_map_lookup_elem(&clustercost_flows, &key);
	if (!stats) {
		struct flow_counters zero = {};
		bpf_map_update_elem(&clustercost_flows, &key, &zero, BPF_ANY);
		stats = bpf_map_lookup_elem(&clustercost_flows, &key);
		if (!stats) {
			return 1;
		}
	}

	if (egress) {
		__sync_fetch_and_add(&stats->tx_bytes, skb->len);
	} else {
		__sync_fetch_and_add(&stats->rx_bytes, skb->len);
	}
	return 1;
}

SEC("cgroup_skb/egress")
int handle_cgroup_egress(struct __sk_buff *skb) {
	return handle_skb(skb, true);
}

SEC("cgroup_skb/ingress")
int handle_cgroup_ingress(struct __sk_buff *skb) {
	return handle_skb(skb, false);
}

char LICENSE[] SEC("license") = "GPL";
