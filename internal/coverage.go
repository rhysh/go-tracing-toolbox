package internal

import (
	"sort"
	"time"

	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

type TimeSpan [2]time.Duration

func newTimeSpan(ev1, ev2 *trace.Event) TimeSpan {
	return TimeSpan{time.Duration(ev1.Ts), time.Duration(ev2.Ts)}
}

func (ts TimeSpan) Start() time.Duration  { return ts[0] }
func (ts TimeSpan) End() time.Duration    { return ts[1] }
func (ts TimeSpan) Length() time.Duration { return ts[1] - ts[0] }

type EventList []*trace.Event

func (list EventList) Extent() TimeSpan {
	return newTimeSpan(list[0], list[len(list)-1])
}

func (list EventList) Running() []TimeSpan {
	var out []TimeSpan
	var start *trace.Event
	for _, ev := range list {
		if start == nil {
			start = ev
		}
		blocked := true
		switch ev.Type {
		default:
			blocked = false
		case trace.EvGoBlock:
		case trace.EvGoBlockSend:
		case trace.EvGoBlockRecv:
		case trace.EvGoBlockSelect:
		case trace.EvGoBlockSync:
		case trace.EvGoBlockCond:
		case trace.EvGoBlockNet:
		case trace.EvGoSysBlock:
		case trace.EvGoBlockGC:
		}
		if blocked {
			out = append(out, newTimeSpan(start, ev))
			start = nil
		}
	}
	return out
}

func (list EventList) BlockNet() []TimeSpan {
	var out []TimeSpan
	var start *trace.Event
	for _, ev := range list {
		if start != nil {
			out = append(out, newTimeSpan(start, ev))
			start = nil
		}
		switch ev.Type {
		case trace.EvGoBlockNet:
			start = ev
		}
	}
	return out
}

func Extent(ts ...TimeSpan) TimeSpan {
	out := ts[0]
	for _, other := range ts[1:] {
		if other[0] < out[0] {
			out[0] = other[0]
		}
		if other[1] > out[1] {
			out[1] = other[1]
		}
	}
	return out
}

// Uncovered calculates how much of the parent range is not covered by at least
// one of the ranges that its children cover.
func Uncovered(parent TimeSpan, children ...TimeSpan) time.Duration {
	var uncovered time.Duration

	sorted := append([]TimeSpan(nil), children...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })

	now := parent[0]
	parentEnd := parent[1]
	for _, child := range sorted {
		childStart := child[0]
		childEnd := child[1]
		if parentEnd < now {
			// The progress marker starts beyond the end of the parent; because
			// the list is sorted, we know we're done.
			break
		}
		if parentEnd < childStart {
			// This child starts beyond the end of the parent; because the list
			// is sorted, we know we're done.
			break
		}
		if now <= childStart {
			// This child starts after a gap in coverage. Calculate the
			// uncovered time.
			uncovered += childStart - now
		}
		if now <= childEnd {
			// This child overlaps with an earlier child, and extends the
			// coverage area.
			now = childEnd
		}
	}

	if now < parentEnd {
		// Mark the tail end of the parent as uncovered.
		uncovered += parentEnd - now
	}

	return uncovered
}
