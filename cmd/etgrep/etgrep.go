package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/rhysh/go-tracing-toolbox/internal/flag2"
	"github.com/rhysh/go-tracing-toolbox/internal/match2"
	"golang.org/x/exp/trace"
)

func main() {
	input := flag.String("input", "", "Path to execution trace file")
	showStacks := flag.Bool("stacks", false, "Show full stack of matching events")
	goroutine := flag.Int64("goroutine", 0, "Filter to events from a single goroutine")
	timestamp := flag.Int64("time", 0, "Filter to events with a specific timestamp")
	sortBy := flag.String("sort", "time", `Sort by "time" or "goroutine"`)

	var match flag2.StackFlag
	flag.Var(&match, "match", `
Event and stack pattern to match. Try 'Any "**"' to match all events.
Try 'StateTransition "net/http...conn..serve" "ServeHTTP" "**" "sync...Mutex..Lock"'
to find stacks where an inbound HTTP request has to wait for a Mutex.`[1:])

	flag.Parse()

	cfg := &config{
		sort:       *sortBy,
		showStacks: *showStacks,
		match:      &match,
		filterGoID: trace.GoID(*goroutine),
		filterTime: trace.Time(*timestamp),
	}

	f, err := os.Open(*input)
	if err != nil {
		log.Fatalf("Open: %v", err)
	}
	defer f.Close()

	reader, err := trace.NewReader(bufio.NewReader(f))
	if err != nil {
		log.Fatalf("trace.NewReader: %v", err)
	}

	var timeEvents []trace.Event

	for {
		ev, err := reader.ReadEvent()
		if err == io.EOF {
			break
		}

		if cfg.sort == "time" {
			str := cfg.printableString(ev)
			if str != "" {
				fmt.Printf("%s", str)
			}
		} else {
			timeEvents = append(timeEvents, ev)
		}
	}

	// TODO: to/from event links

	if *sortBy == "goroutine" {
		goroutineEvents := make(map[trace.GoID][]trace.Event)
		var goroutineList []trace.GoID
		for _, ev := range timeEvents {
			goid := eventGoroutine(ev)
			goroutineEvents[goid] = append(goroutineEvents[goid], ev)
		}
		for goid := range goroutineEvents {
			goroutineList = append(goroutineList, goid)
		}
		sort.Slice(goroutineList, func(i, j int) bool { return goroutineList[i] < goroutineList[j] })

		var prevG trace.GoID
		for _, goid := range goroutineList {
			evs := goroutineEvents[goid]
			for _, ev := range evs {
				str := cfg.printableString(ev)
				if str != "" {
					if prevG != goid {
						if prevG != 0 {
							fmt.Printf("\n")
						}
						prevG = goid
					}
					fmt.Printf("%s", str)
				}
			}
		}
	}
}

type config struct {
	sort       string
	showStacks bool
	match      *flag2.StackFlag
	filterGoID trace.GoID
	filterTime trace.Time
}

func (c *config) printableString(ev trace.Event) string {
	if c.filterGoID != 0 && c.filterGoID != eventGoroutine(ev) {
		return ""
	}
	if c.filterTime != 0 && c.filterTime != ev.Time() {
		return ""
	}
	if c.match.Event != 0 || c.match.Specs != nil {
		if !c.match.EventMatches(ev.Kind()) || !match2.HasStackRe(eventStack(ev), c.match.Specs...) {
			return ""
		}
	}

	str := new(strings.Builder)
	fmt.Fprintf(str, "%s\n", eventString(ev))
	if c.showStacks {
		fmt.Printf("%s", stackString(eventStack(ev)))
	}
	return str.String()
}

func eventGoroutine(ev trace.Event) trace.GoID {
	goid := ev.Goroutine()
	if goid == trace.NoGoroutine {
		if ev.Kind() == trace.EventStateTransition {
			st := ev.StateTransition()
			if st.Resource.Kind == trace.ResourceGoroutine {
				goid = st.Resource.Goroutine()
			}
		}
	}
	return goid
}

func eventStack(ev trace.Event) []runtime.Frame {
	var stack []runtime.Frame
	ev.Stack().Frames(func(f trace.StackFrame) bool {
		stack = append(stack, runtime.Frame{
			Function: f.Func,
			File:     f.File,
			Line:     int(f.Line),
			PC:       uintptr(f.PC),
		})
		return true
	})
	return stack
}

func eventString(ev trace.Event) string {
	str := ev.String()
	first, _, _ := strings.Cut(str, "\n")
	return first
}

func stackString(stk []runtime.Frame) string {
	str := new(strings.Builder)
	for _, frame := range stk {
		fmt.Fprintf(str, "  %x %s %s:%d\n", frame.PC, frame.Function, frame.File, frame.Line)
	}
	return str.String()
}
