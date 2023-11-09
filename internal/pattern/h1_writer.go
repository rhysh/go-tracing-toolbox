package pattern

import (
	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func NewHTTP1WriterTracker() *internal.GeneralTracker {
	// Find the "regions" where we write an outbound HTTP/1.x request.
	//
	//   Start with 'Any "net/http.(*persistConn).writeLoop" "net/http.(*Request).write" "**"'
	//   Or 'Any "net/http.(*persistConn).writeLoop" "**" "net/http.persistConnWriter.Write" "**"'
	//     Followed by an event with a stack that doesn't include those functions
	//
	// The goroutine can have arbitrary interactions while obtaining the Body of
	// the Request. As long as "net/http.(*Request).write" is on the stack, we
	// don't need to worry about the details; we'll identify the end of sending
	// a particular request by the absence of that function in an event with a
	// stack.
	//
	// Make note of the timings.

	stackMatch := func(ev *trace.Event) bool {
		if internal.HasStackRe(ev.Stk, `^net/http...persistConn..writeLoop$`, `^net/http...Request..write$`, "**") {
			return true
		}
		if internal.HasStackRe(ev.Stk, `^net/http...persistConn..writeLoop$`, "**", `^net/http.persistConnWriter.Write$`, "**") {
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
			if ev.Type == trace.EvGoEnd {
				return false
			}
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

func TrackHTTP1Writer(evs []*trace.Event) []*internal.Region {
	var regions []*internal.Region
	track := NewHTTP1WriterTracker()
	track.Flush = func(evs []*trace.Event) {
		regions = append(regions, &internal.Region{Kind: "client/http_write", Events: evs})
	}

	track.Process(evs)

	return regions
}
