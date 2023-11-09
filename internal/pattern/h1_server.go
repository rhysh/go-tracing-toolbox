package pattern

import (
	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func newHTTP1ServerReadRequestTracker() *internal.GeneralTracker {
	// Find the "regions" where we read the headers of an inbound HTTP/1.x
	// request.
	//
	//   Start with 'Any "net/http.(*conn).serve" "net/http.(*conn).readRequest" "**"'
	//   Or    with 'Any "net/http.(*conn).serve" "bufio.(*Reader).Peek" "**"'
	//     Followed by an event with a stack that doesn't include those functions
	//
	// Make note of the timings.

	stackMatch := func(ev *trace.Event) bool {
		return internal.HasStackRe(ev.Stk, `^net/http...conn..serve$`, `^net/http...conn..readRequest$`, "**") ||
			internal.HasStackRe(ev.Stk, `^net/http...conn..serve$`, `^bufio...Reader..Peek$`, "**")
	}

	return &internal.GeneralTracker{
		AllowSingle: true,
		Activate:    func(ev *trace.Event) bool { return stackMatch(ev) },
		Keepalive:   func(ev *trace.Event) bool { return ev.Stk == nil || stackMatch(ev) },
		Critical:    func(ev *trace.Event) bool { return ev.Stk != nil },
	}
}

func newHTTP1ServerWriteResponseTracker() *internal.GeneralTracker {
	// Find the "regions" where we read the headers of an inbound HTTP/1.x
	// request.
	//
	//   Start with 'Any "net/http.(*conn).serve" "net/http.(*response).finishRequest" "**"'
	//     Followed by an event with a stack that doesn't include those functions
	//
	//   And in the case that the goroutine blocks, extend the Region until the goroutine resumes.
	//
	// Make note of the timings.

	stackMatch := func(ev *trace.Event) bool {
		return internal.HasStackRe(ev.Stk, `^net/http...conn..serve$`, `^net/http...response..finishRequest$`, "**")
	}

	return &internal.GeneralTracker{
		AllowSingle: true,
		Activate:    func(ev *trace.Event) bool { return stackMatch(ev) },
		Keepalive:   func(ev *trace.Event) bool { return ev.Stk == nil || stackMatch(ev) },
		Critical:    func(ev *trace.Event) bool { return ev.Stk != nil || ev.Type == trace.EvGoStart },
	}
}

func TrackHTTP1Server(evs []*trace.Event) []*internal.Region {
	var reads []*internal.Region
	{
		track := newHTTP1ServerReadRequestTracker()
		track.Flush = func(evs []*trace.Event) {
			reads = append(reads, &internal.Region{Kind: "server/http_read", Events: evs})
		}
		track.Process(evs)
	}

	var writes []*internal.Region
	{
		track := newHTTP1ServerWriteResponseTracker()
		track.Flush = func(evs []*trace.Event) {
			writes = append(writes, &internal.Region{Kind: "server/http_write", Events: evs})
		}
		track.Process(evs)
	}

	serves := negativeSpace(evs, reads, writes)
	for _, reg := range serves {
		reg.Kind = "server/http"
	}

	var regions []*internal.Region
	regions = append(regions, reads...)
	regions = append(regions, writes...)
	regions = append(regions, serves...)
	return regions
}

// negativeSpace creates Regions from the negative (empty) space between two
// lists of Regions. It assumes that the elements within each list do not
// overlap, though they may share a single event at their boundaries. It assumes
// that the input lists are sorted by start timestamp.
//
// For each Region it returns, the first event will appear as the last event in
// a Region from the starts list, and the last event will appear as the first
// event in the ends list. None of the other events in the Region will appear in
// any input Region.
func negativeSpace(evs []*trace.Event, starts, ends []*internal.Region) []*internal.Region {
	var regions []*internal.Region
	var start *internal.Region
	var queue []*trace.Event
	for _, ev := range evs {
		queue = append(queue, ev)

		for i := range starts {
			sevs := starts[i].Events
			sevN := sevs[len(sevs)-1]
			if sevN == ev {
				// starts[i] is the most recent before this event. Trim it from
				// the list, and try to use its last event as the start of an
				// output Region.
				start, starts = starts[i], starts[i+1:]
				queue = []*trace.Event{ev}
				break
			}
			if sevN.Ts > ev.Ts {
				break
			}
		}

		for len(ends) > 0 {
			eevs := ends[0].Events
			eev0 := eevs[0]
			eevN := eevs[len(eevs)-1]

			if eevN.Ts < ev.Ts {
				// The end Region is fully older than the current event; discard
				// it.
				ends = ends[1:]
				continue
			}
			if eev0.Ts < ev.Ts {
				// The end Region overlaps with the current event; discard it
				// and reset.
				ends = ends[1:]
				start, queue = nil, nil
				continue
			}
			if eev0 == ev {
				// The end Region starts at this event. Keep it.
				if start != nil {
					regions = append(regions, &internal.Region{Events: queue})
					start = nil
				}
				queue = nil
			}
			break
		}
	}

	return regions
}
