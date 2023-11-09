package pattern

import (
	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func NewHTTP1DialerTracker() *internal.GeneralTracker {
	// Find the "regions" where we dial an outbound HTTP/1.x connection.
	//
	//   Start with 'Any "net/http.(*Transport).dialConnFor" "**"'
	//     Followed by an event with a stack that doesn't include that function
	//
	// Make note of the timings.

	stackMatch := func(ev *trace.Event) bool {
		if internal.HasStackRe(ev.Stk, `^net/http...Transport..dialConnFor$`, "**") {
			return true
		}
		return false
	}

	return &internal.GeneralTracker{
		FlushAtEnd: true,
		Activate: func(ev *trace.Event) bool {
			if stackMatch(ev) {
				return true
			}
			return false
		},
		Keepalive: func(ev *trace.Event) bool {
			if ev.Stk == nil {
				return true
			}
			if stackMatch(ev) {
				return true
			}
			return false
		},
	}
}

func TrackHTTP1Dialer(evs []*trace.Event) []*internal.Region {
	var regions []*internal.Region
	track := NewHTTP1DialerTracker()
	track.Flush = func(evs []*trace.Event) {
		regions = append(regions, &internal.Region{Kind: "client/http_dial", Events: evs})
	}

	track.Process(evs)

	return regions
}
