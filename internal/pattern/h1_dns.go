package pattern

import (
	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func NewHTTP1DNSTracker() *internal.GeneralTracker {
	// Find the "regions" where we do a DNS lookup an outbound HTTP/1.x connection.
	//
	//   Start with 'Any ".*" "net.(*Resolver).lookupIPAddr.func1" "**"'
	//     Followed by an event with a stack that doesn't include that function
	//
	// Make note of the timings.

	stackMatch := func(ev *trace.Event) bool {
		if internal.HasStackRe(ev.Stk, ".*", `^net...Resolver..lookupIPAddr.func1$`, "**") {
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
			return true
		},
	}
}

func TrackHTTP1DNS(evs []*trace.Event) []*internal.Region {
	var regions []*internal.Region
	track := NewHTTP1DNSTracker()
	track.Flush = func(evs []*trace.Event) {
		regions = append(regions, &internal.Region{
			Kind:   "client/http_dns",
			Flags:  internal.RegionFlagShared,
			Events: evs,
		})
	}

	track.Process(evs)

	return regions
}
