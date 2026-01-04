VMLINUX ?= /sys/kernel/btf/vmlinux
CLANG ?= clang
.DEFAULT_GOAL := all

ARCH_RAW ?= $(shell uname -m)
ARCH := $(shell if [ "$(ARCH_RAW)" = "x86_64" ]; then echo x86; elif [ "$(ARCH_RAW)" = "aarch64" ]; then echo arm64; else echo $(ARCH_RAW); fi)
VMLINUX_H := vmlinux.h

$(VMLINUX_H):
	@if [ -f $@ ]; then \
		echo "Using existing $@"; \
	elif [ -f $(VMLINUX) ]; then \
		bpftool btf dump file $(VMLINUX) format c > $@; \
	else \
		echo "Error: $(VMLINUX) not found and $@ not present"; \
		exit 1; \
	fi

flows.bpf.o: flows.bpf.c $(VMLINUX_H)
	$(CLANG) -g -O2 -target bpf -D__TARGET_ARCH_$(ARCH) -c $< -o $@

metrics.bpf.o: metrics.bpf.c $(VMLINUX_H)
	$(CLANG) -g -O2 -target bpf -D__TARGET_ARCH_$(ARCH) -c $< -o $@

all: flows.bpf.o metrics.bpf.o

clean:
	rm -f *.bpf.o vmlinux.h
