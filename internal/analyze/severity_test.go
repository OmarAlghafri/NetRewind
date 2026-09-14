package analyze

import (
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

func TestHowLoudlyAnAddressAppearingIsReported(t *testing.T) {
	cases := []struct {
		name         string
		changedHands bool
		isGateway    bool
		want         event.Severity
		why          string
	}{
		{
			name: "a machine genuinely joining the segment",
			want: event.SevInfo,
			why:  "ordinary, and the common case; anything louder would be noise",
		},
		{
			name:         "an address returning on hardware that never held it",
			changedHands: true,
			want:         event.SevWarn,
			why:          "not a first sighting at all - a substitution the failed entry hid",
		},
		{
			name:         "the same, but it is the default gateway",
			changedHands: true,
			isGateway:    true,
			want:         event.SevError,
			why:          "every host on the segment now sends its traffic to a different machine",
		},
		{
			name:      "the gateway seen for the first time",
			isGateway: true,
			want:      event.SevInfo,
			why:       "the recorder starting up meets its gateway; nothing has changed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FirstSightingSeverity(tc.changedHands, tc.isGateway)
			if got != tc.want {
				t.Errorf("severity = %s, want %s — %s", got, tc.want, tc.why)
			}
		})
	}
}
