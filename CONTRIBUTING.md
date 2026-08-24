# Contributing

The most valuable thing you can contribute is **a rule**, and it does not
require writing any Go.

## Contribute a rule

The rule library is the part of this project that cannot be cloned. It
accumulates from real incidents, and anyone who has spent a night debugging a
network has one worth adding. A rule is a YAML file; see
[docs/rules.md](docs/rules.md) for the shape and
[rules/change-broke-a-path.yaml](rules/change-broke-a-path.yaml) for the most
interesting one already shipped.

Before opening a pull request:

```bash
netrewind rules --dir rules      # the same validation the recorder does
go test ./internal/correlate/... # the shipped library is tested too
sudo lab/inject.sh all           # inject faults and see what concludes
```

Three things a rule must have, and the tests enforce two of them:

- **`why` on every clause.** It is the sentence an operator reads instead of
  field names. A clause without one contributes a silent link to the chain.
- **`advice` that says what to look at, never what to change.** The recorder
  observes and does not touch the network, and it does not tell anyone else to
  either. "Restart the interface" would destroy the evidence on a flapping port.
- **An honest `relation`.** `causes` is a claim about mechanism. Reach for it
  only when you could defend it to a sceptical colleague; otherwise
  `correlates`. A rule that overclaims teaches its operator to distrust every
  rule.

## Contribute a collector

New sources are welcome. Two things are non-negotiable:

**Say what you missed.** Every collector that can drop, lag or lose visibility
must report it — `system.drop`, `system.gap`, or a field on its rollup. A
recorder that silently omits what it did not see reports a beautifully quiet
network, which is exactly what an operator concludes when nothing is wrong.

**Put the judgement where it can be tested.** The Linux-only parts should be
plumbing: read the socket, parse the bytes, hand them to something
platform-neutral that decides what they mean.
[`internal/collect/flow`](internal/collect/flow) is the shape — `flow_linux.go`
loads and reads, `tracker.go` decides, and only the second has tests. Every
collector also needs a `_other.go` stub so the tree builds off Linux.

## Working on it

```bash
make all          # fmt, vet, test, build
make lab          # the fault-injection suite (Linux, root)
make kernel-check # what this kernel can actually support
```

Building and testing work on any platform. Observing requires Linux; see
[docs/dev-environment.md](docs/dev-environment.md), which also covers the WSL2
route and the mount-namespace trap that will otherwise cost you an afternoon.

CI runs the fault-injection lab, not only the tests. A green tick means the
thing works, not that it compiles.

## The standard the code is held to

This project's entire claim is that it does not overstate what it knows. That
applies to the code as much as the output:

- **Confidence only falls.** Each stage of the pipeline may weaken a claim,
  never strengthen one. An incident cannot be more certain than the events
  under it.
- **Nothing is recorded that cannot show its evidence.** Every event carries
  enough of the raw observation to justify itself without going back to another
  tool.
- **No payloads, ever.** The recorder observes that a connection was attempted,
  not what was said over it.
- **A known limitation is written down, not papered over.** MAC randomisation
  breaks device tracking; the policy collector polls and can miss a change
  reverted within five seconds; the pair memory is in-process and lost on
  restart. All three are in the docs, because a tool that hides its edges gets
  trusted past them.

Commit messages explain *why*, not what — the diff already says what.

## Licence

AGPL-3.0. By contributing you agree your work is licensed under it. The eBPF
program in `internal/collect/flow/bpf/` is GPL-2.0-or-later, because the kernel
only permits certain helpers to programs declaring a licence it recognises.
