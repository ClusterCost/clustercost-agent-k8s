Build the eBPF objects

This directory contains the eBPF programs required by the agent. Build them on a
Linux host with clang + bpftool installed:

  make

The build emits:
- metrics.bpf.o
- flows.bpf.o

Place the objects at `/opt/clustercost/bpf/` inside the container or set:
- CLUSTERCOST_EBPF_METRICS_OBJECT
- CLUSTERCOST_EBPF_NET_OBJECT
