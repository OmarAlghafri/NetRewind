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
