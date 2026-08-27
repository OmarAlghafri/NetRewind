# Writing a rule

A rule describes one recognisable shape of failure. The engine that reads them
is called Isnad, after the classical Arabic method of establishing a chain of
transmission in which every link is named and every link is verifiable. That is
the standard a rule is held to: it may only claim a cause where it can point at
the events that show one.

Rules are files, not code. The library is the part of this project that cannot
be cloned — it accumulates from real incidents — and anyone who has spent a
night debugging a network can contribute to it without touching Go.

```bash
netrewind rules --dir rules      # load and check the library
```

## The shape of a rule

```yaml
id: gateway-hijack
title: The default gateway is being answered by a different machine
severity: error          # info | notice | warn | error
confidence: 90           # 1-100, an upper bound - see below
window: 180s             # how far apart the first and last clause may be
correlate_on: [subject]  # fields every matched event must agree on
root_cause: hijack       # which clause the rule blames, by its `as`
advice: >-
  What to look at next. Never what to change: the recorder observes and does
  not touch the network.
match:
  - as: hijack
    kinds: [l2.arp_binding_changed]
    where:
      attrs:
        is_gateway: "true"
    why: >-
      The sentence this step contributes to the account. It is what the
      operator reads instead of the field names.

  - as: reroute
    kinds: [l3.default_route_changed]
    optional: true
    relation: causes
    why: Routing followed the change.
```

## Clauses match in time order

The clauses are a sequence, not a set. The engine looks for the first clause,
then the second *after* it, and so on, all inside `window`. A consequence
arriving before its cause is not the shape the rule describes, and the engine
will not pretend otherwise.

`min_count` requires a clause to match several times. One port going down is an
event; the same port going down five times in five minutes is a fault, and only
the second is worth waking someone for.

`optional: true` lets the rule fire without a clause. Use it for consequences
that sometimes follow and sometimes do not.

When an optional clause arrives after the rule has already fired, the incident
**grows**: the fuller account replaces the thinner one rather than appearing
beside it as a near-duplicate.

## Asking what changed *before* it broke

The first **required** clause is the rule's anchor: the event whose arrival makes
the engine evaluate the rule at all. Clauses after it are searched forwards in
time. Clauses **before** it are searched backwards — nearest first.

That is how a rule asks the question this whole project exists for. The symptom
is what arrives: a pair that stopped connecting, a route that vanished. The
useful question is what changed just before it.

```yaml
match:
  - as: change                              # optional, searched BACKWARDS
    kinds: [l2.arp_binding_changed, l3.route_changed, link.down]
    optional: true
    why: This is the last thing that changed on the path before it broke.

  - as: breakage                            # required - the anchor
    kinds: [flow.first_failure_for_pair]
    relation: causes
    why: Two machines that had been connecting can no longer complete a handshake.
```

With no preceding change in the window the rule still fires, with a one-link
chain: the symptom stands on its own. With one, the chain opens on the cause.

The nearest change is chosen deliberately: an older one would be a worse guess
wearing the same claim. This is also why such a rule's `advice` must say plainly
that the named change is the nearest in time, not a proven cause.

Every rule needs at least one required clause. A rule where all of them are
optional would match everything, and the validator rejects it.

## Relation is the whole discipline

Every clause after the first must say how it relates to the one before it:

| Relation | The claim |
|---|---|
| `causes` | A mechanism. This event is *why* the next one happened. |
| `correlates` | They moved together. No mechanism is claimed. |
| `precedes` | Only ordering in time. |

Anything can notice that two things happened close together. Saying which of
them caused the other is a much stronger claim, and it has to be earned by a
rule whose author encoded the mechanism.

Reach for `causes` only when you could defend it to a sceptical colleague. A
tool that blurs correlation into causation teaches its operator to distrust it,
and an operator who distrusts the timeline is back to guessing — which is the
condition this whole project exists to end.

## Correlation keys keep unrelated failures apart

`correlate_on` names fields that every matched event must agree on:

- `subject` — the label an operator knows the entity by
- `subject.id` — the resolved stable identity
- `attrs.<name>` — any kind-specific field
- `observer` — which recorder saw it

Without it, two failures happening at the same moment on different machines get
woven into one incident that never happened. With `correlate_on: [subject]`, a
port flapping on one switch cannot be joined to a neighbour failing on another.

## Confidence is an upper bound

The number in the rule is how far *its author* trusts the inference. The engine
then lowers it to the least certain event in the chain.

An incident resting on an inference cannot be more certain than the inference.
A rule that is sure of itself, built on an ARP binding the collector was only
80% sure of, is an 80% incident.

## Narrowing with `where`

```yaml
where:
  attrs:
    is_gateway: "true"     # compared as text - a collector may write a
    is_default: "true"     # bool, and the store returns json.Number
  min_severity: warn
  subject_kind: host
```

## Advice says what to look at, never what to do

Every rule in the library ends with advice, and a test enforces that it has
some. The recorder does not change anything on the network and does not tell
anyone else to. It points at the next place to look:

> Find which switch port the new hardware address is learned on before changing
> anything. A failover looks identical to an attack from here; the port tells
> them apart.

That is useful. "Restart the interface" would not be — and on a flapping port
it would destroy the evidence.

## Checking your work

```bash
netrewind rules --dir rules              # validates every file
go test ./internal/correlate/...         # the shipped library is tested too
sudo lab/inject.sh all                   # inject faults, see what concludes
```

The validator refuses a rule that cannot work: one with no window, one that
blames a clause it does not have, one whose second clause does not say how it
relates to the first, and one in which *every* clause is optional — that rule
has no anchor to fire on and would match everything. It caught exactly that
last mistake in one of the rules shipped here, which would otherwise have sat
in the library firing on anything that moved.
