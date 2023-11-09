package pattern

import (
	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func NewHTTP1ReaderTracker() *internal.GeneralTracker {
	// Find the "regions" where we read an inbound HTTP/1.x response.
	//
	//   Start with 'Any "**" "net/http.(*persistConn).readLoop" "**"'
	//     Followed by an event with a stack that doesn't include that function
	//
	// Make note of the timings.

	stackMatch := func(ev *trace.Event) bool {
		if internal.HasStackRe(ev.Stk, `^net/http...persistConn..readLoop$`, "**", `^net/http...persistConn..Read$`, "**") {
			return true
		}
		return false
	}

	return &internal.GeneralTracker{
		AllowSingle: true,
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

func TrackHTTP1Reader(evs []*trace.Event) []*internal.Region {
	var regions []*internal.Region
	track := NewHTTP1ReaderTracker()
	track.Flush = func(evs []*trace.Event) {
		regions = append(regions, &internal.Region{Kind: "client/http_read", Events: evs})
	}

	track.Process(evs)

	return regions
}
