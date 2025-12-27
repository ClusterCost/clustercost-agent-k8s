// SPDX-License-Identifier: GPL-2.0

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

struct flow_key {
	__u8 src_addr[16];
	__u8 dst_addr[16];
	__u8 family;
	__u8 proto;
	__u8 pad[2];
};

struct flow_stats {
	__u64 tx_bytes;
	__u64 rx_bytes;
};

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 65536);
	__type(key, struct flow_key);
	__type(value, struct flow_stats);
} clustercost_flows SEC(".maps");

static __always_inline void fill_ipv4(__u8 dst[16], __u32 addr) {
	*(__u32 *)dst = addr;
}

static __always_inline void fill_ipv6(__u8 dst[16], const __u8 *src) {
	#pragma unroll
	for (int i = 0; i < 16; i++) {
		dst[i] = src[i];
	}
}

static __always_inline int handle_skb(struct __sk_buff *skb, bool egress) {
	struct flow_key key = {};
	struct flow_stats *stats;

	__u16 proto = bpf_ntohs(skb->protocol);
	if (proto == ETH_P_IP) {
		struct iphdr iph;
		if (bpf_skb_load_bytes(skb, 0, &iph, sizeof(iph)) < 0) {
			return 1;
		}
		key.family = 2; // AF_INET
		key.proto = iph.protocol;
		fill_ipv4(key.src_addr, iph.saddr);
		fill_ipv4(key.dst_addr, iph.daddr);
	} else if (proto == ETH_P_IPV6) {
		struct ipv6hdr iph6;
		if (bpf_skb_load_bytes(skb, 0, &iph6, sizeof(iph6)) < 0) {
			return 1;
		}
		key.family = 10; // AF_INET6
		key.proto = iph6.nexthdr;
		fill_ipv6(key.src_addr, iph6.saddr.in6_u.u6_addr8);
		fill_ipv6(key.dst_addr, iph6.daddr.in6_u.u6_addr8);
	} else {
		return 1;
	}

	stats = bpf_map_lookup_elem(&clustercost_flows, &key);
	if (!stats) {
		struct flow_stats zero = {};
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
