package pattern

import (
	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func NewHTTPClientTracker() *internal.GeneralTracker {
	// Find the "regions" where we make an outbound HTTP request.
	//
	//   Start with 'Any "**" "net/http.(*Transport).RoundTrip" "**"'
	//     Followed by an event with a stack that doesn't include that function
	//
	// Make note of the timings.

	return &internal.GeneralTracker{
		Activate: func(ev *trace.Event) bool {
			if internal.HasStackRe(ev.Stk, `**`, `^net/http...Transport..RoundTrip$`, "**") {
				return true
			}
			return false
		},
		Keepalive: func(ev *trace.Event) bool {
			if ev.Stk == nil {
				return true
			}
			if internal.HasStackRe(ev.Stk, `**`, `^net/http...Transport..RoundTrip$`, "**") {
				return true
			}
			return false
		},
		Critical: func(ev *trace.Event) bool {
			if ev.Type == trace.EvGoStart {
				return true
			}
			if internal.HasStackRe(ev.Stk, `**`, `^net/http...Transport..RoundTrip$`, "**") {
				return true
			}
			return false
		},
	}
}

func TrackHTTPClient(evs []*trace.Event) []*internal.Region {
	var regions []*internal.Region
	track := NewHTTPClientTracker()
	track.Flush = func(evs []*trace.Event) {
		regions = append(regions, &internal.Region{Kind: "client/http_roundtrip", Events: evs})
	}

	track.Process(evs)

	return regions
}
