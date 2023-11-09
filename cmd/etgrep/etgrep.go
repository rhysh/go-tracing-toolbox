package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func main() {
	input := flag.String("input", "", "Path to execution trace file")
	showStacks := flag.Bool("stacks", false, "Show full stack of matching events")
	goroutine := flag.Uint64("goroutine", 0, "Filter to events from a single goroutine")

	var match internal.StackFlag
	flag.Var(&match, "match", `
Event and stack pattern to match. Try 'Any "**"' to match all events.
Try 'GoBlockSync "net/http...conn..serve" "ServeHTTP" "**" "sync...Mutex..Lock"'
to find stacks where an inbound HTTP request has to wait for a Mutex.`[1:])

	flag.Parse()

	data, err := func(name string) (*internal.Data, error) {
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		result, err := trace.Parse(bufio.NewReader(f), "")
		if err != nil {
			return nil, err
		}
		data := internal.PrepareData(result.Events)
		return data, nil
	}(*input)
	if err != nil {
		log.Fatalf("Parse; err = %v", err)
	}

	checkMatch := match.Event != 0 || match.Specs != nil

	var prevG uint64
	for _, g := range data.GoroutineList {
		if *goroutine != 0 && *goroutine != g {
			continue
		}
		evs := data.GoroutineEvents[g]
		for _, ev := range evs {
			if checkMatch && (!match.EventMatches(ev.Type) || !internal.HasStackRe(ev.Stk, match.Specs...)) {
				continue
			}
			if prevG != 0 && prevG != ev.G {
				fmt.Printf("\n")
			}
			prevG = ev.G

			var others []string
			if prev := data.Backlinks[ev]; prev != nil && prev.G != ev.G {
				others = append(others, fmt.Sprintf("from %s", prev))
			}
			if next := ev.Link; next != nil {
				others = append(others, fmt.Sprintf("to %s", next))
			}
			more := ""
			if len(others) > 0 {
				more = " (" + strings.Join(others, ", ") + ")"
			}
			fmt.Printf("%s%s\n", ev, more)

			if *showStacks {
				for _, frame := range ev.Stk {
					fmt.Printf("  %x %s %s:%d\n", frame.PC, frame.Fn, frame.File, frame.Line)
				}
			}
		}
	}
}
