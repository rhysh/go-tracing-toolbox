package internal

import (
	"fmt"
	"log"
	"strings"

	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

// A Region is a contiguous series of Events on a single goroutine to accomplish
// a particular goal.
type Region struct {
	Kind   string
	Flags  int64
	Events []*trace.Event
}

const (
	RegionFlagShared = 0x1
)

// Shared returns whether the Region represents work that will be shared with
// unrelated goroutines.
func (r *Region) Shared() bool { return (r.Flags & 0x1) == 0x1 }

// A RegionStack is an immutable linked list of Regions and inbound
// communication events that explain why the program is currently doing a unit
// of work.
type RegionStack struct {
	// Start marks when this goroutine began the current explanation for its
	// work.
	Start *trace.Event
	// Local is a Region on this goroutine that explains its current work. If
	// this goroutine is working on behalf of another and does not have an
	// annotation for its own work, this field will be nil.
	Local *Region
	// Parent links to the higher-level reason for the current work.
	Parent *RegionStack
}

func (rs *RegionStack) DebugString() string {
	buf := new(strings.Builder)
	for link := rs; link != nil; link = link.Parent {
		if link.Local != nil {
			if buf.Len() > 0 {
				fmt.Fprintf(buf, " ")
			}
			fmt.Fprintf(buf, "%q/%d", link.Local.Kind, link.Local.Flags)
		}
	}
	return buf.String()
}

// A Task is a set of Regions (across any number of goroutines) that work
// together to accomplish a particular goal.
type Task struct {
	Region   *Region
	Children []*Task
}

type TrackerState func(*trace.Event) TrackerState

type GeneralTracker struct {
	// Activate returns whether the event marks the start of a new Region.
	Activate func(ev *trace.Event) bool
	// Keepalive returns whether the event indicates the Region is still active.
	Keepalive func(ev *trace.Event) bool
	// Critical returns whether the event is a critical part of the Region
	// lifecycle. Events that are not critical to a Region's lifecycle are
	// trimmed from the end.
	Critical func(ev *trace.Event) bool
	// Reactivate controls whether the GeneralTracker finds the largest or
	// smallest possible Regions. When true, the GeneralTracker will try to
	// start a new Region for any Event that matches the Activate function. When
	// false, it tries to add Events to an existing Region.
	Reactivate bool

	// AllowSingle enables single-event Regions. Otherwise, the smallest
	// possible Region is 2 events.
	AllowSingle bool

	// FlushAtEnd attempts to write out any partial Region at the goroutine's
	// final Event.
	FlushAtEnd bool

	Verbose bool

	// Flush is called with the Events that make up a single Region.
	Flush func([]*trace.Event)

	trimFrom int
	queue    []*trace.Event
}

// flushEvent is a sentinel for the goroutine having no more Events.
var flushEvent = new(trace.Event)

func (t *GeneralTracker) Process(evs []*trace.Event) {
	state := t.idle
	for _, ev := range evs {
		state = state(ev)
	}
	state(flushEvent)
}

func (t *GeneralTracker) log(format string, v ...interface{}) {
	if t.Verbose {
		log.Printf(format, v...)
	}
}

func (t *GeneralTracker) idle(ev *trace.Event) TrackerState {
	if ev == flushEvent {
		return nil
	}
	if fn := t.Activate; fn != nil && fn(ev) {
		t.log("idle->active   %s", ev)
		t.queue = append(t.queue, ev)
		if fn := t.Critical; fn != nil && fn(ev) {
			t.trimFrom = len(t.queue)
			t.log("save %d", t.trimFrom)
		}
		return t.active
	}

	t.log("idle->idle     %s", ev)
	return t.idle
}

// active processes an Event as a continuation of an open region.
func (t *GeneralTracker) active(ev *trace.Event) TrackerState {
	done := func() {
		if fn := t.Flush; fn != nil {
			if t.Critical == nil {
				t.trimFrom = len(t.queue)
			}
			t.log("flush [0:%d] %q %q", t.trimFrom, t.queue[0:t.trimFrom], t.queue[t.trimFrom:])
			t.queue = t.queue[0:t.trimFrom]
			if len(t.queue) >= 2 || (t.AllowSingle && len(t.queue) >= 1) {
				fn(t.queue)
			}
		}

		t.trimFrom = 0
		t.queue = nil
	}

	if ev == flushEvent {
		if t.FlushAtEnd {
			done()
		}
		return nil
	}

	active := true

	if fn := t.Keepalive; fn == nil || !fn(ev) {
		t.log("active->idle   %s", ev)
		done()
		active = false
	}

	if fn := t.Activate; t.Reactivate && t.Critical != nil && fn != nil && fn(ev) {
		t.log("reactivate     %s", ev)
		done()
		active = true
	}

	if !active {
		return t.idle
	}

	t.queue = append(t.queue, ev)

	t.log("active->active %s", ev)
	if fn := t.Critical; fn != nil && fn(ev) {
		t.trimFrom = len(t.queue)
		t.log("save %d", t.trimFrom)
	}
	return t.active
}

// The events in an execution trace form a graph. Each event may be connected to
// up to four others:
//   - the event on the same goroutine immediately prior
//   - the event on the same goroutine immediately following
//   - a GoStart event on another goroutine that this unblocked
//   - an event on another goroutine that unblocked this GoStart
//
// Note that we use the Link field only when the target is a GoStart event. The
// cross-goroutine Link values that we ignore include when the current goroutine
// is starting to block, and will be made runnable by the event that Link
// references: we capture that connection later, as a back-reference from the
// current goroutine's GoStart event.
//
// An event can "cause" others in the future, and can "benefit from" others in
// the past.
//
// Here's an example from when an HTTP request needs to dial a new connection,
// and also needs to do a DNS lookup.
//
//   - The RoundTrip goroutine creates a dialConnFor goroutine.
//   - The dialConnFor goroutine creates a DNS orchestration goroutine.
//   - The DNS orchestration goroutine creates A and AAAA goroutines.
//   - Each resolution goroutine communicates back to the orchestrator.
//   - Then dialConnFor creates a goroutine to create the TCP connection.
//   - And then dialConnFor creates a goroutine to do the TLS handshake.
//   - And then it creates the readLoop and writeLoop goroutines.
//   - The RoundTrip goroutine communicates with the writeLoop goroutine.
//   - The readLoop goroutine communicates with the RoundTrip goroutine.
//
// The results of the DNS lookup may be shared with other dialConnFor
// goroutines. That means that they "benefit from" a part of the work that the
// original RoundTrip goroutine "caused". That's a strange link to create.
//
// We can search forward in time to find all events that a starting event
// "caused". And we can search backward in time to find all events that an
// ending event "benefitted from". We can do that for the start and end of a
// particular region on one goroutine, take the union of those sets, and get a
// list of events that the region both "caused" and "benefitted from".
//
// But on the other hand, when a RoundTrip goroutine is able to immediately
// reuse a fresh connection, it doesn't have any communication with the readLoop
// goroutine until the complete response headers are ready. So it's strange to
// not recognize its role in completing that request.
//
// And on top of that, a higher-level operation that makes two HTTP requests to
// the same backend might use the same connection for both, and in between those
// two requests the connection might do useful work for a different high-level
// operation. So it's also strange to have any "reason for being" remain
// permanent.
//
// Instead, we work in chronological order across all goroutines, tracking what
// work each is going at every moment.
//
// First, we find the kinds of work that are of interest to us. We create a
// Region for each, representing a contiguous list of events on a single
// goroutine.
//
// A Region starting on a goroutine immediately changes that goroutine's "reason
// for being":
//
// - In some cases it adds more information as a key feature. When a writeLoop
// goroutine enters the Region that describes writing the HTTP request to the
// network, that work is still operating on behalf of a goroutine (and
// corresponding Region) that is in a RoundTrip call.
//
// - In some cases it adds more information as flavor. When a TLS-handshaking
// goroutine enters the Region that describes the TLS handshake, the goroutine
// still exists because a particular RoundTrip call demanded it. The resulting
// connection may be used by that RoundTrip call, handed off to another, or put
// into the connection pool for later use.
//
// - In some cases it needs to not add information. A DNS-resolution goroutine
// may be created for a particular RoundTrip call, but its results may be reused
// for other concurrent RoundTrip calls. When it allows those dialConnFor
// goroutines to continue, it should not overwrite their "reasons for being".
//
// - Even when the DNS-resolution goroutine unblocks the dialConnFor goroutine
// that created it, it must not change that goroutine's "reason for being".
// Follow-up work that dialConnFor does, such as the TLS handshake or writeLoop
// goroutine, should be because "RoundTrip call X needed connection dial Y", and
// not mention the "DNS request Z".
//
// The Region.Shared method reports whether the Region represents work that we
// know is shared. That prevents it from overwriting the "reason for being" of
// the goroutines it communicates with, unless they haven't yet found their
// purpose.
//
// The other defense we have against incorrectly applying "reasons for being" is
// to refuse to overwrite a Region that is currently active on the goroutine.
