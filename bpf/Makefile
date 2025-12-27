VMLINUX ?= /sys/kernel/btf/vmlinux
CLANG ?= clang
LLC ?= llc
ARCH ?= $(shell uname -m)

TARGET := $(shell uname -m)

VMLINUX_H := vmlinux.h

VMLINUX_H: $(VMLINUX)
	bpftool btf dump file $(VMLINUX) format c > $(VMLINUX_H)

flows.bpf.o: flows.bpf.c $(VMLINUX_H)
	$(CLANG) -g -O2 -target bpf -D__TARGET_ARCH_$(TARGET) -c $< -o $@

metrics.bpf.o: metrics.bpf.c $(VMLINUX_H)
	$(CLANG) -g -O2 -target bpf -D__TARGET_ARCH_$(TARGET) -c $< -o $@

all: flows.bpf.o metrics.bpf.o

clean:
	rm -f *.bpf.o vmlinux.h
