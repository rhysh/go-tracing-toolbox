package internal

import (
	"sort"

	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

type ProcessRegionConnectionsInput struct {
	Data    *Data
	Regions []*Region
}

type ProcessRegionConnectionsResult struct {
	EventRegionStacks map[*trace.Event]*RegionStack
}

type RegionConnector struct {
	// StartRegion sets the new RegionStack of a goroutine when it starts a new
	// local Region. If left unset, the RegionConnector uses a default behavior
	// of adding the starting RegionStack on top of the existing RegionStack.
	StartRegion func(ev *trace.Event, existing, starting *RegionStack) *RegionStack
	// ApplyOnWake sets the new RegionStack for a goroutine when it is made
	// runnable by another goroutine's event ev. The GoStart event for the
	// now-runnable goroutine is ev.Link. If left unset, the RegionConnector
	// uses a default behavior of trimming the inbound RegionStack back to
	ApplyOnWake func(ev *trace.Event, existing, inbound *RegionStack) *RegionStack
}

func (rc *RegionConnector) DoStartRegion(ev *trace.Event, existing, starting *RegionStack) *RegionStack {
	if rc != nil && rc.StartRegion != nil {
		return rc.StartRegion(ev, existing, starting)
	} else {
		// By default, assume that the newly-started regions are
		// compatible with the existing regions. Reference the existing
		// regions.

		var head, tail *RegionStack
		for node := starting; node != nil; node = node.Parent {
			next := &RegionStack{Start: node.Start, Local: node.Local, Parent: nil}
			if head == nil {
				head = next
			}
			if tail != nil {
				tail.Parent = next
			}
			tail = next
		}
		tail.Parent = existing
		return head
	}
}

func (rc *RegionConnector) DoApplyOnWake(ev *trace.Event, existing, inbound *RegionStack) *RegionStack {
	if rc != nil && rc.ApplyOnWake != nil {
		return rc.ApplyOnWake(ev, existing, inbound)
	} else {
		if inbound == nil {
			return existing
		}

		propose := &RegionStack{Start: ev.Link, Parent: inbound}
		for try := propose; try != nil; try = try.Parent {
			// When a goroutine is doing "shared" work, it may only apply its
			// regions to goroutines that have no other explanation for their
			// work.
			if existing != nil && try.Local != nil && try.Local.Shared() {
				return existing
			}

			if try.Start.G == ev.Link.G {
				propose = try
			}
			if try.Local != nil && try.Local.Events[0].G == ev.Link.G {
				// What about when the region has completed?
				propose = try
			}
		}
		if existing == nil || existing.Local == nil {
			return propose
		}

		return existing
	}
}

func (rc *RegionConnector) Process(in *ProcessRegionConnectionsInput) *ProcessRegionConnectionsResult {
	out := &ProcessRegionConnectionsResult{
		EventRegionStacks: make(map[*trace.Event]*RegionStack, len(in.Data.Events)),
	}

	goroutineRegions := make(map[uint64][]*Region)
	starts := make(map[*trace.Event][]*Region)
	ends := make(map[*trace.Event][]*Region)

	for _, region := range in.Regions {
		ev0 := region.Events[0]
		goroutineRegions[ev0.G] = append(goroutineRegions[ev0.G], region)
		starts[ev0] = append(starts[ev0], region)
		evN := region.Events[len(region.Events)-1]
		ends[evN] = append(ends[evN], region)
	}

	for ev, fresh := range starts {
		// Sort smaller regions to the front. Regions are required to involve a
		// single goroutine, and to include a contiguous set of Events. These
		// Regions start at the same Event, so we can determine which ends
		// earlier by looking at the number of events they contain.
		sort.Slice(fresh, func(i, j int) bool {
			fi, fj := fresh[i], fresh[j]
			if li, lj := len(fi.Events), len(fj.Events); li != lj {
				return li < lj
			}
			// TODO: find a good way to order Regions with identical bounds
			if fi.Kind != "client/http_roundtrip" {
				return true
			}
			return false
		})
		starts[ev] = fresh
	}

	stackNow := make(map[uint64]*RegionStack)

	for _, ev := range in.Data.Events {
		why := stackNow[ev.G]

		// Deal with newly-created regions
		freshList := starts[ev]
		var fresh *RegionStack
		for i := len(freshList) - 1; i >= 0; i-- {
			fresh = &RegionStack{Start: ev, Local: freshList[i], Parent: fresh}
		}
		if fresh != nil {
			why = rc.DoStartRegion(ev, why, fresh)
		}

		// Apply regions to peers
		if ev.Link != nil && ev.Link.Type == trace.EvGoStart && ev.G != ev.Link.G {
			stackNow[ev.Link.G] = rc.DoApplyOnWake(ev, stackNow[ev.Link.G], why)
		}

		// Remove local regions that ended at this event
		staleSet := make(map[*Region]struct{})
		for _, region := range ends[ev] {
			staleSet[region] = struct{}{}
		}
		// But keep track of any local Regions that are still open
		var activeLocal []*Region
		for link := why; link != nil; link = link.Parent {
			if link.Local != nil && link.Start.G == ev.G &&
				link.Local.Events[len(link.Local.Events)-1].Ts > ev.Ts {
				activeLocal = append(activeLocal, link.Local)
			}
			if _, ok := staleSet[link.Local]; ok {
				why = link.Parent
				for i := len(activeLocal) - 1; i >= 0; i-- {
					local := activeLocal[i]
					why = &RegionStack{Local: local, Parent: why, Start: local.Events[0]}
				}
			}
		}

		out.EventRegionStacks[ev] = why
		stackNow[ev.G] = why
	}

	return out
}
