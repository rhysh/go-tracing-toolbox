package internal

import (
	"fmt"
	"sort"

	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

type Data struct {
	// Result holds the event list from a basic execution trace parse result.
	// The events are chronological order.
	Events []*trace.Event
	// GoroutineEvents maps each observed goroutine to that goroutine's events.
	GoroutineEvents map[uint64][]*trace.Event
	// GoroutineList lists all known goroutines by ID
	GoroutineList []uint64
	// Backlinks holds events that are the targets of other events' Link field.
	Backlinks map[*trace.Event]*trace.Event
	// GoroutineRuns maps GoStart events to the sequence of events on that
	// goroutine that followed, until the goroutine stopped running.
	GoroutineRuns map[*trace.Event][]*trace.Event

	// Prev maps an event to the preceding event on the same goroutine.
	Prev map[*trace.Event]*trace.Event
	// Next maps an event to the subsequent event on the same goroutine.
	Next map[*trace.Event]*trace.Event
}

func PrepareData(events []*trace.Event) *Data {
	var data Data
	data.Events = make([]*trace.Event, len(events))
	copy(data.Events, events)

	// Sort events by timestamp
	sort.Slice(data.Events, func(i, j int) bool {
		ei, ej := data.Events[i], data.Events[j]
		return ei.Ts < ej.Ts
	})

	// Map goroutines to their events
	data.GoroutineEvents = make(map[uint64][]*trace.Event)
	for _, ev := range data.Events {
		data.GoroutineEvents[ev.G] = append(data.GoroutineEvents[ev.G], ev)
	}
	for g := range data.GoroutineEvents {
		data.GoroutineList = append(data.GoroutineList, g)
	}
	sort.Slice(data.GoroutineList, func(i, j int) bool { return data.GoroutineList[i] < data.GoroutineList[j] })
	data.Prev = make(map[*trace.Event]*trace.Event)
	data.Next = make(map[*trace.Event]*trace.Event)
	for _, g := range data.GoroutineList {
		evs := data.GoroutineEvents[g]
		sort.Slice(evs, func(i, j int) bool { return evs[i].Ts < evs[j].Ts })

		var prev *trace.Event
		for _, ev := range evs {
			if prev != nil {
				data.Prev[ev] = prev
				data.Next[prev] = ev
			}
			prev = ev
		}
	}

	data.Backlinks = make(map[*trace.Event]*trace.Event)
	for _, ev := range data.Events {
		if ev.Link != nil {
			if prev := data.Backlinks[ev.Link]; prev != nil {
				panic(fmt.Sprintf("double link on %q: %q and %q", ev.Link, ev, prev))
			}
			data.Backlinks[ev.Link] = ev
		}
	}

	data.GoroutineRuns = make(map[*trace.Event][]*trace.Event)
	for _, g := range data.GoroutineList {
		var start *trace.Event
		// makes a copy of the events slice - maybe we could share
		for _, ev := range data.GoroutineEvents[g] {
			if ev.Type == trace.EvGoStart {
				start = ev
			}
			if start != nil {
				data.GoroutineRuns[start] = append(data.GoroutineRuns[start], ev)
			}
		}
	}

	return &data
}
