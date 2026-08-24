package event

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Describe renders an event as a sentence a network engineer can read at three
// in the morning without consulting the schema.
//
// This is the first layer of the narrative the whole project is for. The
// correlation engine will later chain these sentences into an account of an
// incident; getting them right one event at a time is what makes that account
// readable rather than a wall of field names.
func Describe(e *Event) string {
	switch e.Kind {
	case KindLinkDown:
		switch Str(e, "cause") {
		case "administrative":
			return fmt.Sprintf("%s was shut down administratively", e.Subject.Label)
		case "removed":
			return fmt.Sprintf("%s was removed", e.Subject.Label)
		default:
			return fmt.Sprintf("%s lost carrier", e.Subject.Label)
		}

	case KindLinkUp:
		if ms, ok := Int(e, "down_duration_ms"); ok {
			return fmt.Sprintf("%s came back after %s", e.Subject.Label,
				(time.Duration(ms) * time.Millisecond).Round(time.Millisecond))
		}
		return fmt.Sprintf("%s came up", e.Subject.Label)

	case KindLinkFlap:
		n, _ := Int(e, "transitions")
		return fmt.Sprintf("%s has gone down %d times - the link itself is failing", e.Subject.Label, n)

	case KindLinkErrorRate:
		return fmt.Sprintf("%s is losing %v%% of its packets to errors", e.Subject.Label,
			Str(e, "error_rate_percent"))

	case KindFlowReset:
		return fmt.Sprintf("%s reset the connection from %s on port %s",
			Str(e, "dst"), Str(e, "src"), Str(e, "dport"))

	case KindFlowTimeoutNoClose:
		s := fmt.Sprintf("the connection from %s to %s port %s died without being closed",
			Str(e, "src"), Str(e, "dst"), Str(e, "dport"))
		if ms, ok := Int(e, "lifetime_ms"); ok {
			s += fmt.Sprintf(" after %s", (time.Duration(ms) * time.Millisecond).Round(time.Second))
		}
		return s

	case KindMetricAnomaly:
		target := Str(e, "target")
		switch Str(e, "metric") {
		case "unreachable":
			return fmt.Sprintf("%s stopped answering entirely", target)
		case "packet_loss":
			return fmt.Sprintf("%v%% of probes to %s went unanswered", Str(e, "loss_percent"), target)
		case "latency":
			ms, _ := Int(e, "rtt_ms")
			base, _ := Int(e, "baseline_ms")
			return fmt.Sprintf("the round trip to %s rose from %dms to %dms", target, base, ms)
		case "recovered":
			return fmt.Sprintf("%s is answering again", target)
		default:
			return fmt.Sprintf("%s changed measurably", target)
		}

	case KindLinkMTUChanged:
		old, _ := Int(e, "mtu_old")
		nw, _ := Int(e, "mtu_new")
		return fmt.Sprintf("%s MTU changed from %d to %d", e.Subject.Label, old, nw)

	case KindARPBindingNew:
		return fmt.Sprintf("%s first answered from %s", e.Subject.Label, Str(e, "mac"))

	case KindARPBindingChanged:
		s := fmt.Sprintf("%s moved from %s to %s", e.Subject.Label, Str(e, "mac_old"), Str(e, "mac_new"))
		if Bool(e, "is_gateway") {
			s += " - this is the default gateway"
		}
		return s

	case KindDuplicateIP:
		return fmt.Sprintf("%s is being claimed by more than one machine", e.Subject.Label)

	case KindMACMoved:
		return fmt.Sprintf("%s appeared behind a different interface", Str(e, "mac"))

	case KindNeighborFailed:
		return fmt.Sprintf("%s stopped answering at layer 2", e.Subject.Label)

	case KindRouteAdded:
		if gw := Str(e, "gateway"); gw != "" {
			return fmt.Sprintf("route to %s added via %s", e.Subject.Label, gw)
		}
		return fmt.Sprintf("route to %s added", e.Subject.Label)

	case KindRouteRemoved:
		if Bool(e, "is_default") {
			return "the default route was removed - nothing beyond the local segment is reachable"
		}
		return fmt.Sprintf("route to %s was removed", e.Subject.Label)

	case KindRouteChanged:
		return fmt.Sprintf("route to %s now goes via %s (was %s)",
			e.Subject.Label, Str(e, "gateway_new"), Str(e, "gateway_old"))

	case KindDefaultRouteChanged:
		return fmt.Sprintf("the default route now goes via %s (was %s)",
			Str(e, "gateway_new"), Str(e, "gateway_old"))

	case KindAddrAdded:
		return fmt.Sprintf("%s was added to the interface", Str(e, "address"))

	case KindAddrRemoved:
		return fmt.Sprintf("%s was removed from the interface", Str(e, "address"))

	case KindFlowHandshakeFail:
		return fmt.Sprintf("connections to %s port %s went unanswered",
			e.Subject.Label, Str(e, "dport"))

	case KindFlowFirstFailureForPair:
		s := fmt.Sprintf("%s can no longer reach %s on port %s",
			Str(e, "src"), Str(e, "dst"), Str(e, "dport"))
		if ms, ok := Int(e, "last_success_ago_ms"); ok {
			s += fmt.Sprintf(" - it worked %s ago",
				(time.Duration(ms) * time.Millisecond).Round(time.Second))
		}
		return s

	case KindFlowRollup:
		opened, _ := Int(e, "opened")
		closed, _ := Int(e, "closed")
		failed, _ := Int(e, "handshake_failures")
		return fmt.Sprintf("%d connections opened, %d closed, %d unanswered", opened, closed, failed)

	case KindDHCPServerSeen:
		s := fmt.Sprintf("a DHCP server answered from %s", Str(e, "server"))
		if n, ok := Int(e, "other_servers"); ok && n > 0 {
			s = fmt.Sprintf("a second DHCP server appeared on %s", Str(e, "server"))
			if gw := Str(e, "offers_gateway"); gw != "" {
				s += fmt.Sprintf(", handing out %s as the gateway", gw)
			}
		}
		return s

	case KindDHCPOffer:
		return fmt.Sprintf("%s was offered to %s", Str(e, "address"), Str(e, "client_mac"))

	case KindDHCPAck:
		return fmt.Sprintf("%s was confirmed to %s", Str(e, "address"), Str(e, "client_mac"))

	case KindDHCPNak:
		return fmt.Sprintf("%s was refused the address it asked for", Str(e, "client_mac"))

	case KindDHCPLeaseChanged:
		return fmt.Sprintf("%s moved from %s to %s",
			Str(e, "client_mac"), Str(e, "address_old"), Str(e, "address_new"))

	case KindDNSResolverChanged:
		return fmt.Sprintf("%s started asking %s instead of %s",
			Str(e, "client"), Str(e, "resolver_new"), Str(e, "resolver_old"))

	case KindDNSQueryFail:
		if name := Str(e, "name"); name != "" {
			return fmt.Sprintf("%s returned %s for %s", Str(e, "resolver"), Str(e, "rcode"), name)
		}
		return fmt.Sprintf("%s returned %s", Str(e, "resolver"), Str(e, "rcode"))

	case KindDNSLatencySpike:
		if ms, ok := Int(e, "took_ms"); ok {
			return fmt.Sprintf("%s took %s to answer", Str(e, "resolver"),
				(time.Duration(ms) * time.Millisecond).Round(time.Millisecond))
		}
		return fmt.Sprintf("%s was slow to answer", Str(e, "resolver"))

	case KindICMPUnreachable:
		return fmt.Sprintf("%s reported %s for %s",
			Str(e, "reported_by"), Str(e, "reason"), Str(e, "destination"))

	case KindMTUBlackhole:
		return fmt.Sprintf("the path to %s needs fragmentation at %s bytes - ping works, large transfers do not",
			Str(e, "destination"), Str(e, "next_hop_mtu"))

	case KindPolicyRuleChanged:
		added, _ := Int(e, "added")
		removed, _ := Int(e, "removed")
		switch {
		case added > 0 && removed > 0:
			return fmt.Sprintf("the filtering rules changed: %d added, %d removed", added, removed)
		case added > 0:
			return fmt.Sprintf("%d filtering rule(s) were added", added)
		case removed > 0:
			return fmt.Sprintf("%d filtering rule(s) were removed", removed)
		default:
			return "the filtering rules changed"
		}

	case KindSystemGap:
		if ms, ok := Int(e, "gap_duration_ms"); ok {
			return fmt.Sprintf("the recorder was not watching for %s",
				(time.Duration(ms) * time.Millisecond).Round(time.Second))
		}
		return "the recorder was not watching for a period"

	case KindSystemDrop:
		if n, ok := Int(e, "dropped"); ok {
			return fmt.Sprintf("%d events were lost to a full buffer", n)
		}
		return "events were lost to a full buffer"

	case KindSystemClockStep:
		if ms, ok := Int(e, "step_ms"); ok {
			return fmt.Sprintf("the clock jumped by %s",
				(time.Duration(ms) * time.Millisecond).Round(time.Millisecond))
		}
		return "the clock jumped"

	case KindCollectorDown:
		if name, ok := e.Attrs["collector"].(string); ok {
			return fmt.Sprintf("the %s source stopped feeding the record; nothing of what it watches was seen after this", name)
		}
		return "a source stopped feeding the record"

	case KindSystemStart:
		return "the recorder started"

	case KindSystemStop:
		return "the recorder stopped"
	}

	return string(e.Kind) + " on " + e.Subject.Label
}

// Str reads a string attribute, returning "" when it is absent.
func Str(e *Event, key string) string {
	switch v := e.Attrs[key].(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

// Int reads a numeric attribute.
//
// It has to cope with both the native value a collector supplied and the
// json.Number the store hands back, which is why every caller goes through
// here rather than asserting a type.
func Int(e *Event, key string) (int64, bool) {
	switch v := e.Attrs[key].(type) {
	case nil:
		return 0, false
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

// Bool reads a boolean attribute.
func Bool(e *Event, key string) bool {
	switch v := e.Attrs[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	default:
		return false
	}
}
