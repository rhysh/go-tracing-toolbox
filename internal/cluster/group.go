package cluster

import (
	"fmt"
	"sort"

	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

// A Span describes a single goroutine's contribution of a unit of useful work.
type Span struct {
	// G is the id of the goroutine that executed this Span
	G uint64
	// Kind names this type of Span
	Kind string
	// StartNs is the start time of this Span measured in nanoseconds.
	StartNs int64
	// LengthNs is the duration of this Span measured in nanoseconds.
	LengthNs int64

	// StartRun is a list of times when the goroutine started running. It is
	// measured in nanoseconds relative to StartNs.
	StartRun []int64

	// StartAssist describes the starts of stretches of time when the goroutine
	// was running, but was not running its own code. An entry in the "gc" list
	// here describes when the goroutine started doing GC Assist work.
	StartAssist map[string][]int64

	// StartWait describes the starts of stretches of time when the goroutine
	// was not running because it was waiting for some kind of event. An entry
	// in the "net" list here describes when the goroutine started blocking
	// because it needed to read data from the network. An entry in the "cpu"
	// list here describes when the goroutine becomes runnable.
	StartWait map[string][]int64

	// Caused lists Spans that this Span caused to exist.
	Caused []*Span
}

// A TreeSummary describes the overall behavior of a tree of Spans.
type TreeSummary struct {
	// LengthNs is the duration of this tree of Spans measured in nanoseconds.
	LengthNs int64

	// TotalRunNs is the total amount of CPU time these Spans consumed.
	TotalRunNs int64
	// TotalAssistNs is the total amount of CPU time these Spans spent doing
	// non-application work.
	TotalAssistNs map[string]int64

	// FlatRunNs is the amount of wall-clock time attributable to on-CPU time.
	//
	// When collapsing a set of timelines (one for each Span) into a single
	// timeline, time ranges where any Span was on-CPU counts as on-CPU time in
	// the single flattened timeline.
	//
	// Similarly, time ranges there at least one Span was doing assist work
	// counts as time doing assist work in the flattened view, in addition to
	// being on-CPU time. Priority for attributing assist work starts with "gc"
	// assist.
	//
	// Time ranges where every Span is waiting are counted as waiting in the
	// flat view. Priority is as follows:
	//  - "cpu" is first, to follow the high preference given to on-CPU time.
	//  - "gc" is second, again because it's getting in the way of what would be on-CPU time.
	//  - "net", as it's likely to represent forces outside of the process.
	//  - "syscall", again because it's likely to show us interesting cross-process waiting.
	//  - Finally, process-internal synchronization: "select", "recv", "send", "cond", "sync", "block".
	FlatRunNs    int64
	FlatAssistNs map[string]int64
	FlatWaitNs   map[string]int64

	Flat *Span
	Root *Span
}

type Ranges struct {
	Running   [][2]int64
	Assisting map[string][][2]int64
	Waiting   map[string][][2]int64
}

// Running returns time ranges relative to the Span's start when the Span was
// running, when it was blocked for various reasons, and when it was doing
// assist work.
func Running(span *Span) (*Ranges, error) {
	type change struct {
		ts     int64
		wait   string
		assist string
	}
	var changes []change
	for _, ts := range span.StartRun {
		changes = append(changes, change{ts: ts})
	}

	assists := make(map[int64]string)
	for reason, list := range span.StartAssist {
		if reason == "" {
			return nil, fmt.Errorf("cluster.Running: blank assist reason")
		}
		for _, ts := range list {
			if prev, ok := assists[ts]; ok {
				return nil, fmt.Errorf("cluster.Running: duplicate assist reasons %q and %q at %d", prev, reason, ts)
			}
			assists[ts] = reason
			changes = append(changes, change{ts: ts, assist: reason})
		}
	}

	waits := make(map[int64]string)
	for reason, list := range span.StartWait {
		if reason == "" {
			return nil, fmt.Errorf("cluster.Running: blank wait reason")
		}
		for _, ts := range list {
			if prev, ok := waits[ts]; ok {
				return nil, fmt.Errorf("cluster.Running: duplicate wait reasons %q and %q at %d", prev, reason, ts)
			}
			waits[ts] = reason
			changes = append(changes, change{ts: ts, wait: reason})
		}
	}

	sort.Slice(changes, func(i, j int) bool { return changes[i].ts < changes[j].ts })

	out := &Ranges{
		Assisting: make(map[string][][2]int64),
		Waiting:   make(map[string][][2]int64),
	}
	for i, change := range changes {
		end := span.LengthNs
		if i < len(changes)-1 {
			end = changes[i+1].ts
		}

		v := [2]int64{change.ts, end}

		if reason := change.assist; reason != "" {
			out.Running = append(out.Running, v)
			out.Assisting[reason] = append(out.Assisting[reason], v)
			continue
		}
		if reason := change.wait; reason != "" {
			out.Waiting[reason] = append(out.Waiting[reason], v)
			continue
		}
		out.Running = append(out.Running, v)
	}

	window := [2]int64{0, span.LengthNs}
	if cpu := span.StartWait["cpu"]; len(cpu) > 0 && cpu[0] < 0 {
		window[0] = cpu[0]
	}
	out.Running = collapse(out.Running, window)
	for reason, tss := range out.Assisting {
		out.Assisting[reason] = collapse(tss, window)
	}
	for reason, tss := range out.Waiting {
		out.Waiting[reason] = collapse(tss, window)
	}

	return out, nil
}

func Summarize(root *Span) *TreeSummary {
	summary := &TreeSummary{
		LengthNs:      root.LengthNs,
		TotalAssistNs: make(map[string]int64),
		FlatAssistNs:  make(map[string]int64),
		FlatWaitNs:    make(map[string]int64),
		Flat: &Span{
			G:           root.G,
			Kind:        root.Kind,
			StartNs:     root.StartNs,
			LengthNs:    root.LengthNs,
			StartAssist: make(map[string][]int64),
			StartWait:   make(map[string][]int64),
		},
		Root: root,
	}

	// TODO: should the window correspond to the root Span's wall-clock time (to
	// show us how much work is required to give a response), or should it also
	// include work done after the fact?
	window := [2]int64{root.StartNs, root.StartNs + root.LengthNs}

	// A goroutine's work can show up in multiple Spans. Make sure to only count
	// the work once.
	gruns := make(map[uint64][][2]int64)
	gassists := make(map[uint64]map[string][][2]int64)
	gwaits := make(map[uint64]map[string][][2]int64)

	assistPref := []string{"gc", "other"}
	waitPref := []string{
		"cpu",
		"gc",
		"net",
		"syscall",
		"select", "recv", "send", "cond", "sync", "block",
		"other",
	}

	Visit(root, func(span *Span) {
		ranges, err := Running(span)
		if err != nil {
			return
		}

		adjustOne := func(add int64, pair [2]int64) [2]int64 {
			pair = [2]int64{
				pair[0] + add,
				pair[1] + add,
			}
			return pair
		}
		adjustAll := func(add int64, tss [][2]int64) [][2]int64 {
			ours := make([][2]int64, 0, len(tss))
			for _, pair := range tss {
				ours = append(ours, adjustOne(add, pair))
			}
			return ours
		}

		ranges.Running = adjustAll(span.StartNs, ranges.Running)
		for reason, tss := range ranges.Assisting {
			ranges.Assisting[reason] = adjustAll(span.StartNs, tss)
		}
		for reason, tss := range ranges.Waiting {
			ranges.Waiting[reason] = adjustAll(span.StartNs, tss)
		}

		gruns[span.G] = append(gruns[span.G], ranges.Running...)

		m, ok := gassists[span.G]
		if !ok {
			m = make(map[string][][2]int64)
			gassists[span.G] = m
		}
		for reason, tss := range ranges.Assisting {
			m[reason] = append(m[reason], tss...)
		}

		m, ok = gwaits[span.G]
		if !ok {
			m = make(map[string][][2]int64)
			gwaits[span.G] = m
		}
		for reason, tss := range ranges.Waiting {
			m[reason] = append(m[reason], tss...)
		}
	})

	// A goroutine that is Assisting is also Running. But when we calculate the
	// flat time spent in Assist, we want to first remove the time when any
	// goroutine was running and not assisting, and then observe the times
	// during which at least one goroutine was assisting.

	var allRuns, nonAssistRuns [][2]int64
	for g, tss := range gruns {
		tss = collapse(tss, window)

		gruns[g] = tss
		allRuns = append(allRuns, tss...)
		summary.TotalRunNs += magnitude(tss)

		nonAssist := tss
		for _, v := range gassists[g] {
			nonAssist = subtract(tss, v)
		}
		nonAssistRuns = append(nonAssistRuns, nonAssist...)
	}
	allRuns = collapse(allRuns, window)
	nonAssistRuns = collapse(nonAssistRuns, window)

	allAssists := make(map[string][][2]int64)
	for g, m := range gassists {
		for reason, tss := range m {
			tss = collapse(tss, window)
			gassists[g][reason] = tss
		}
		for _, reason := range assistPref {
			tss := gassists[g][reason]
			delete(gassists[g], reason)
			allAssists[reason] = append(allAssists[reason], tss...)
			summary.TotalAssistNs[reason] += magnitude(tss)
		}
		for _, tss := range gassists[g] {
			reason := "other"
			allAssists[reason] = append(allAssists[reason], tss...)
			summary.TotalAssistNs[reason] += magnitude(tss)
		}
	}
	for reason, tss := range allAssists {
		allAssists[reason] = collapse(tss, window)
	}

	allWaits := make(map[string][][2]int64)
	for g, m := range gwaits {
		for reason, tss := range m {
			tss = collapse(tss, window)
			gwaits[g][reason] = tss
		}
		for _, reason := range waitPref {
			tss := gwaits[g][reason]
			delete(gwaits[g], reason)
			allWaits[reason] = append(allWaits[reason], tss...)
		}
		for _, tss := range gwaits[g] {
			reason := "other"
			allWaits[reason] = append(allWaits[reason], tss...)
		}
	}
	for reason, tss := range allWaits {
		allWaits[reason] = collapse(tss, window)
	}

	summary.FlatRunNs = magnitude(allRuns)
	for _, pair := range allRuns {
		summary.Flat.StartRun = append(summary.Flat.StartRun, pair[0]-root.StartNs)
	}

	remainder := [][2]int64{window}
	remainder = subtract(remainder, nonAssistRuns)
	unaccounted := magnitude(remainder)

	for _, reason := range assistPref {
		tss := allAssists[reason]
		delete(allAssists, reason)

		for _, pair := range subtract(tss, not(remainder, window)) {
			summary.Flat.StartAssist[reason] = append(summary.Flat.StartAssist[reason], pair[0]-root.StartNs)
		}

		remainder = subtract(remainder, tss)
		after := magnitude(remainder)

		summary.FlatAssistNs[reason] = unaccounted - after
		unaccounted = after
	}

	for _, reason := range waitPref {
		tss := allWaits[reason]
		delete(allWaits, reason)

		for _, pair := range subtract(tss, not(remainder, window)) {
			summary.Flat.StartWait[reason] = append(summary.Flat.StartWait[reason], pair[0]-root.StartNs)
		}

		remainder = subtract(remainder, tss)
		after := magnitude(remainder)

		summary.FlatWaitNs[reason] = unaccounted - after
		unaccounted = after
	}

	for k, v := range summary.TotalAssistNs {
		if v == 0 {
			delete(summary.TotalAssistNs, k)
		}
	}
	for k, v := range summary.FlatAssistNs {
		if v == 0 {
			delete(summary.FlatAssistNs, k)
		}
	}
	for k, v := range summary.FlatWaitNs {
		if v == 0 {
			delete(summary.FlatWaitNs, k)
		}
	}

	return summary
}

// AllRunning returns time ranges relative to the Span's start when any children
// of the span were running and when any children of the span were blocked on
// the network.
func AllRunning(root *Span) (run, net [][2]int64, runSum int64) {
	Visit(root, func(span *Span) {
		ranges, err := Running(span)
		if err != nil {
			return
		}

		// TODO: update summary to include all wait/assist reasons

		sr := ranges.Running
		sn := ranges.Waiting["net"]

		for i := range sr {
			runSum += sr[i][1] - sr[i][0]
			sr[i][0] += span.StartNs - root.StartNs
			sr[i][1] += span.StartNs - root.StartNs
		}
		for i := range sn {
			sn[i][0] += span.StartNs - root.StartNs
			sn[i][1] += span.StartNs - root.StartNs
		}
		run = append(run, sr...)
		net = append(net, sn...)
	})

	window := [2]int64{0, root.LengthNs}
	run = collapse(run, window)
	net = collapse(net, window)

	return run, net, runSum
}

func collapse(ranges [][2]int64, window [2]int64) [][2]int64 {
	byStart := make([][2]int64, 0, len(ranges))
	for _, v := range ranges {
		if v[0] < v[1] {
			byStart = append(byStart, v)
		}
	}
	sort.Slice(byStart, func(i, j int) bool { return byStart[i][0] < byStart[j][0] })
	byEnd := append([][2]int64(nil), byStart...)
	sort.Slice(byEnd, func(i, j int) bool { return byEnd[i][1] < byEnd[j][1] })

	var start, end int64
	var out [][2]int64
	for i, v := range byStart {
		if i == 0 {
			start, end = v[0], v[1]
			continue
		}
		if v[0] > end {
			out = append(out, [2]int64{start, end})
			start, end = v[0], v[1]
			continue
		}
		if v[1] > end {
			end = v[1]
		}
	}
	if len(byStart) > 0 {
		out = append(out, [2]int64{start, end})
	}

	var keep [][2]int64
	for _, v := range out {
		if v[1] <= window[0] {
			continue
		}
		if v[0] >= window[1] {
			continue
		}
		if v[0] < window[0] {
			v[0] = window[0]
		}
		if v[1] > window[1] {
			v[1] = window[1]
		}
		keep = append(keep, v)
	}

	return keep
}

func not(ranges [][2]int64, window [2]int64) [][2]int64 {
	ranges = collapse(ranges, window)
	notRanges := make([][2]int64, 0, len(ranges)+1)
	start := window[0]
	for _, v := range ranges {
		notRanges = append(notRanges, [2]int64{start, v[0]})
		start = v[1]
	}
	notRanges = append(notRanges, [2]int64{start, window[1]})
	return collapse(notRanges, window)
}

func subtract(base, delta [][2]int64) [][2]int64 {
	var window [2]int64
	for i, v := range base {
		if i == 0 {
			window = v
			continue
		}
		if v[0] < window[0] {
			window[0] = v[0]
		}
		if v[1] > window[1] {
			window[1] = v[1]
		}
	}

	notBase := not(base, window)
	notResult := append([][2]int64(nil), notBase...)
	notResult = append(notResult, delta...)
	result := not(notResult, window)

	return result
}

func magnitude(ranges [][2]int64) int64 {
	var sum int64
	for _, v := range ranges {
		sum += v[1] - v[0]
	}
	return sum
}

func Visit(span *Span, fn func(span *Span)) {
	fn(span)
	for _, sp := range span.Caused {
		Visit(sp, fn)
	}
}

func ExtractSpans(data *internal.Data, findRegions func([]*trace.Event) []*internal.Region) []*Span {
	rootFunc := make(map[uint64]string)
	for _, g := range data.GoroutineList {
		var fn string
		for _, ev := range data.GoroutineEvents[g] {
			if l := len(ev.Stk); l >= 1 {
				fn = ev.Stk[l-1].Fn
				if l >= 2 {
					// Sometimes the one-frame starting stack shows up as a
					// wrapper function. Wait for a better one if it's
					// available. See https://golang.org/issue/50622.
					break
				}
			}
		}
		rootFunc[g] = fn
	}

	var regions []*internal.Region
	for _, g := range data.GoroutineList {
		regions = append(regions, findRegions(data.GoroutineEvents[g])...)
	}

	var rc internal.RegionConnector
	out := rc.Process(&internal.ProcessRegionConnectionsInput{Data: data, Regions: regions})

	firstSaw := make(map[*internal.RegionStack]*trace.Event)
	for _, ev := range data.Events {
		stack := out.EventRegionStacks[ev]
		if stack == nil {
			continue
		}
		if _, ok := firstSaw[stack]; ok {
			continue
		}

		firstSaw[stack] = ev
	}

	type tree struct {
		parent   *tree
		children []*tree
		self     *internal.RegionStack
	}

	rootFromRegion := make(map[*internal.RegionStack]*internal.RegionStack)
	for stack := range firstSaw {
		for try := stack; try != nil; try = try.Parent {
			if root, ok := rootFromRegion[try]; ok {
				try = root
			}
			if try.Parent == nil {
				rootFromRegion[stack] = try
				break
			}
		}
	}
	regionsFromRoot := make(map[*internal.RegionStack][]*internal.RegionStack)
	for child, root := range rootFromRegion {
		if child != root { // TODO: what's the purpose of this condition?
			regionsFromRoot[root] = append(regionsFromRoot[root], child)
		}
	}
	treeFromStack := make(map[*internal.RegionStack]*tree)
	for root, children := range regionsFromRoot {
		treeFromStack[root] = &tree{self: root}
		for _, child := range children {
			treeFromStack[child] = &tree{self: child}
		}
	}
	for _, node := range treeFromStack {
		for link := node.self.Parent; link != nil; link = link.Parent {
			// Account for Regions that are doubled / exactly overlapping; find
			// the true parent.
			if link.Start != node.self.Start {
				node.parent = treeFromStack[link]
				break
			}
		}

		if node.parent != nil {
			node.parent.children = append(node.parent.children, node)
		}
	}
	for _, node := range treeFromStack {
		sort.Slice(node.children, func(i, j int) bool {
			ci, cj := node.children[i].self, node.children[j].self
			return ci.Start.Ts < cj.Start.Ts
		})
	}

	var roots []*internal.RegionStack
	for root := range regionsFromRoot {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Start.Ts < roots[j].Start.Ts })

	var spans []*Span
	for _, root := range roots {
		var visit func(t *tree, fn func(*tree))
		visit = func(t *tree, fn func(*tree)) {
			fn(t)
			for _, child := range t.children {
				visit(child, fn)
			}
		}

		spanFromTree := make(map[*tree]*Span)

		t := treeFromStack[root]
		visit(t, func(node *tree) {
			stack := node.self
			depth := 0
			for try := stack; try != nil; try = try.Parent {
				if try != stack {
					depth++
				}
			}
			name := rootFunc[stack.Start.G]
			if stack.Local != nil {
				name = stack.Local.Kind
			}
			span := &Span{
				G:       stack.Start.G,
				Kind:    name,
				StartNs: stack.Start.Ts,

				StartAssist: make(map[string][]int64),
				StartWait:   make(map[string][]int64),
			}
			spanFromTree[node] = span
			if parent, ok := spanFromTree[node.parent]; ok {
				parent.Caused = append(parent.Caused, span)
			}

			var evs []*trace.Event
			for ev := stack.Start; ev != nil; ev = data.Next[ev] {
				evStack := out.EventRegionStacks[ev]
				var ok bool
				for try := evStack; try != nil; try = try.Parent {
					if try == stack {
						ok = true
					}
				}
				evs = append(evs, ev)
				// The final event in a region doesn't keep the RegionStack
				// marker. Add that one last event before bailing out.
				if !ok {
					break
				}
			}

			startEvents := map[byte]struct{}{
				trace.EvGoStart:          {},
				trace.EvGCMarkAssistDone: {},
			}
			assistEvents := map[byte]string{
				trace.EvGCMarkAssistStart: "gc",
			}
			waitEvents := map[byte]string{
				trace.EvGoPreempt:     "cpu",
				trace.EvGoBlock:       "block",
				trace.EvGoBlockCond:   "cond",
				trace.EvGoBlockGC:     "gc",
				trace.EvGoBlockNet:    "net",
				trace.EvGoBlockRecv:   "recv",
				trace.EvGoBlockSelect: "select",
				trace.EvGoBlockSend:   "send",
				trace.EvGoBlockSync:   "sync",
				trace.EvGoSysBlock:    "syscall",
			}

			changeEv := make([]*trace.Event, 0, len(evs))
			for _, ev := range evs {
				_, ok1 := startEvents[ev.Type]
				_, ok2 := assistEvents[ev.Type]
				_, ok3 := waitEvents[ev.Type]
				if ok1 || ok2 || ok3 || len(changeEv) == 0 {
					changeEv = append(changeEv, ev)
				}
			}

			for i := 0; i < len(changeEv); i++ {
				ev := changeEv[i]
				var next *trace.Event
				if i < len(changeEv)-1 {
					next = changeEv[i+1]
				}
				// When a non-root Span begins with a GoStart event, the time it
				// took to schedule that goroutine means delay for the root
				// Span. Count that as "cpu" wait time before the Span's zero
				// time.
				if i == 0 && ev.Type == trace.EvGoStart {
					if prev := data.Backlinks[ev]; prev != nil {
						wait := span.StartNs - prev.Ts
						if wait > 0 {
							span.StartWait["cpu"] = append(span.StartWait["cpu"], -wait)
						}
					}
				}

				if reason, ok := assistEvents[ev.Type]; ok {
					span.StartAssist[reason] = append(span.StartAssist[reason], ev.Ts-span.StartNs)
					continue
				}
				if reason, ok := waitEvents[ev.Type]; ok {
					span.StartWait[reason] = append(span.StartWait[reason], ev.Ts-span.StartNs)
					if ev.Link != nil && next != nil && ev.Ts < ev.Link.Ts && ev.Link.Ts < next.Ts {
						span.StartWait["cpu"] = append(span.StartWait["cpu"], ev.Link.Ts-span.StartNs)
					}
					continue
				}
				span.StartRun = append(span.StartRun, ev.Ts-span.StartNs)
			}

			span.LengthNs = evs[len(evs)-1].Ts - stack.Start.Ts
		})
		rootSpan := spanFromTree[t]
		spans = append(spans, rootSpan)
	}

	return spans
}
