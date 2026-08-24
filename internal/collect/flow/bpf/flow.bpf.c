// SPDX-License-Identifier: GPL-2.0-or-later
//
// TCP state transitions, observed in the kernel.
//
// This file alone is offered under GPL-2.0-or-later rather than the project's
// AGPL-3.0. The kernel only permits eBPF programs to call certain helpers when
// they declare a licence it recognises as GPL-compatible, and AGPL is not on
// that list. GPL-2.0-or-later is compatible with the rest of the project.
//
// It hooks one stable tracepoint rather than a kprobe on purpose: kprobes break
// silently when the kernel renames or inlines a function, and a recorder that
// stops recording without saying so is the failure mode this whole project
// exists to prevent.

#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>

#define AF_INET 2
#define IPPROTO_TCP 6

// The tracepoint argument layout is a kernel ABI, so it is declared here rather
// than pulled from a generated vmlinux.h. That keeps the build free of a 3 MB
// generated header and free of a bpftool dependency, and the layout is checked
// against /sys/kernel/tracing/events/sock/inet_sock_set_state/format by
// lab/check-kernel.sh.
struct inet_sock_set_state_args {
	unsigned short common_type;
	unsigned char common_flags;
	unsigned char common_preempt_count;
	int common_pid;

	const void *skaddr;
	int oldstate;
	int newstate;
	__u16 sport;
	__u16 dport;
	__u16 family;
	__u16 protocol;
	__u8 saddr[4];
	__u8 daddr[4];
	__u8 saddr_v6[16];
	__u8 daddr_v6[16];
};

// Which hook produced an event. Two are needed because the state tracepoint
// alone cannot tell a reset from a timeout: a connection killed by a received
// RST and one that simply gave up retransmitting both go from ESTABLISHED
// straight to CLOSE, and "the peer refused" and "the path disappeared" are
// completely different faults to be told apart at three in the morning.
#define EV_STATE 0
#define EV_RESET 1

// flow_event is one observation about a connection. It carries no payload and
// no hostname: the recorder observes that a connection was attempted, not what
// was said over it.
struct flow_event {
	__u64 ts_ns;
	__u32 saddr;
	__u32 daddr;
	__u16 sport;
	__u16 dport;
	__u8 oldstate;
	__u8 newstate;
	__u16 family;
	__u8 kind;
	__u8 pad[3];
};

// The tcp_receive_reset argument layout, checked against
// /sys/kernel/tracing/events/tcp/tcp_receive_reset/format.
struct tcp_receive_reset_args {
	unsigned short common_type;
	unsigned char common_flags;
	unsigned char common_preempt_count;
	int common_pid;

	const void *skaddr;
	__u16 sport;
	__u16 dport;
	__u16 family;
	__u8 saddr[4];
	__u8 daddr[4];
	__u8 saddr_v6[16];
	__u8 daddr_v6[16];
	__u64 sock_cookie;
};

// events carries transitions to userspace. One mebibyte absorbs a burst; when
// it cannot, the loss is counted rather than ignored.
struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 1 << 20);
} events SEC(".maps");

// dropped counts transitions that did not fit in the ring buffer.
//
// This is the single most important map in the program. A recorder that
// silently loses events reports a quiet network, and a quiet network is exactly
// what an operator concludes when nothing is wrong. Counting the loss turns a
// lie into a system.drop event.
struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__type(key, __u32);
	__type(value, __u64);
	__uint(max_entries, 1);
} dropped SEC(".maps");

SEC("tracepoint/sock/inet_sock_set_state")
int trace_inet_sock_set_state(struct inet_sock_set_state_args *ctx)
{
	if (ctx->protocol != IPPROTO_TCP)
		return 0;
	// IPv6 transitions are dropped for now rather than half-recorded: the
	// address would not fit the event and a truncated address is worse than an
	// absent one.
	if (ctx->family != AF_INET)
		return 0;

	struct flow_event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
	if (!e) {
		__u32 key = 0;
		__u64 *n = bpf_map_lookup_elem(&dropped, &key);
		if (n)
			__sync_fetch_and_add(n, 1);
		return 0;
	}

	e->ts_ns = bpf_ktime_get_ns();
	e->kind = EV_STATE;
	e->oldstate = (__u8)ctx->oldstate;
	e->newstate = (__u8)ctx->newstate;
	e->sport = ctx->sport;
	e->dport = ctx->dport;
	e->family = ctx->family;
	e->pad[0] = 0;
	e->pad[1] = 0;
	e->pad[2] = 0;
	__builtin_memcpy(&e->saddr, ctx->saddr, 4);
	__builtin_memcpy(&e->daddr, ctx->daddr, 4);

	bpf_ringbuf_submit(e, 0);
	return 0;
}

// A reset arriving is the difference between "they refused" and "it went
// quiet", and userspace pairs it with the state change that follows.
SEC("tracepoint/tcp/tcp_receive_reset")
int trace_tcp_receive_reset(struct tcp_receive_reset_args *ctx)
{
	if (ctx->family != AF_INET)
		return 0;

	struct flow_event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
	if (!e) {
		__u32 key = 0;
		__u64 *n = bpf_map_lookup_elem(&dropped, &key);
		if (n)
			__sync_fetch_and_add(n, 1);
		return 0;
	}

	e->ts_ns = bpf_ktime_get_ns();
	e->kind = EV_RESET;
	e->oldstate = 0;
	e->newstate = 0;
	e->sport = ctx->sport;
	e->dport = ctx->dport;
	e->family = ctx->family;
	e->pad[0] = 0;
	e->pad[1] = 0;
	e->pad[2] = 0;
	__builtin_memcpy(&e->saddr, ctx->saddr, 4);
	__builtin_memcpy(&e->daddr, ctx->daddr, 4);

	bpf_ringbuf_submit(e, 0);
	return 0;
}

char LICENSE[] SEC("license") = "GPL";
