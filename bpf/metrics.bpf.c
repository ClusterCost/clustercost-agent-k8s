// SPDX-License-Identifier: GPL-2.0

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_tracing.h>

struct metric_key {
	__u64 cgroup_id;
};

struct metric_stats {
	__u64 cpu_time_ns;
	__u64 memory_bytes;
};

struct cpu_state {
	__u64 last_ts;
	__u32 last_pid;
};

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 65536);
	__type(key, struct metric_key);
	__type(value, struct metric_stats);
} clustercost_metrics SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 131072);
	__type(key, __u32);
	__type(value, __u64);
} pid_start SEC(".maps");

static __always_inline void add_cpu_time(__u64 cgroup_id, __u64 delta) {
	struct metric_key key = {.cgroup_id = cgroup_id};
	struct metric_stats *stats = bpf_map_lookup_elem(&clustercost_metrics, &key);
	if (!stats) {
		struct metric_stats zero = {};
		bpf_map_update_elem(&clustercost_metrics, &key, &zero, BPF_ANY);
		stats = bpf_map_lookup_elem(&clustercost_metrics, &key);
		if (!stats) {
			return;
		}
	}
	__sync_fetch_and_add(&stats->cpu_time_ns, delta);
}

SEC("tracepoint/sched/sched_switch")
int handle_sched_switch(struct trace_event_raw_sched_switch *ctx) {
	__u64 now = bpf_ktime_get_ns();
	__u32 prev_pid = ctx->prev_pid;
	__u32 next_pid = ctx->next_pid;

	if (prev_pid != 0) {
		__u64 *start = bpf_map_lookup_elem(&pid_start, &prev_pid);
		if (start && now > *start) {
			__u64 delta = now - *start;
			__u64 cgroup_id = bpf_get_current_cgroup_id();
			add_cpu_time(cgroup_id, delta);
		}
	}

	bpf_map_update_elem(&pid_start, &next_pid, &now, BPF_ANY);
	return 0;
}

static __always_inline void add_memory(__u64 cgroup_id, __s64 delta) {
	struct metric_key key = {.cgroup_id = cgroup_id};
	struct metric_stats *stats = bpf_map_lookup_elem(&clustercost_metrics, &key);
	if (!stats) {
		struct metric_stats zero = {};
		bpf_map_update_elem(&clustercost_metrics, &key, &zero, BPF_ANY);
		stats = bpf_map_lookup_elem(&clustercost_metrics, &key);
		if (!stats) {
			return;
		}
	}
	if (delta >= 0) {
		__sync_fetch_and_add(&stats->memory_bytes, (__u64)delta);
	} else {
		__sync_fetch_and_sub(&stats->memory_bytes, (__u64)(-delta));
	}
}

SEC("tracepoint/mm/mm_page_alloc")
int handle_mm_page_alloc(struct trace_event_raw_mm_page_alloc *ctx) {
	__u64 cgroup_id = bpf_get_current_cgroup_id();
	__u64 bytes = ((__u64)1 << ctx->order) * 4096;
	add_memory(cgroup_id, bytes);
	return 0;
}

SEC("tracepoint/mm/mm_page_free")
int handle_mm_page_free(struct trace_event_raw_mm_page_free *ctx) {
	__u64 cgroup_id = bpf_get_current_cgroup_id();
	__u64 bytes = ((__u64)1 << ctx->order) * 4096;
	add_memory(cgroup_id, -(__s64)bytes);
	return 0;
}

char LICENSE[] SEC("license") = "GPL";
