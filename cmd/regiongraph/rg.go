package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
	"github.com/rhysh/go-tracing-toolbox/internal/cluster"
	"github.com/rhysh/go-tracing-toolbox/internal/pattern"
)

func main() {
	input := flag.String("input", "", "Path to execution trace file")
	showChains := flag.Bool("show-chains", false, "Print full chains of inter-region connections")
	showPairs := flag.Bool("show-pairs", false, "Print pairs of inter-region connections")
	showRegions := flag.Bool("show-regions", false, "Print regions")
	showJSON := flag.Bool("json", false, "Print clusters in JSON format (subject to change)")
	summarize := flag.Bool("summarize", false, "Use a summary in the JSON format")
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
		regions = append(regions, pattern.TrackAll(data.GoroutineEvents[g])...)
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

	stackString := func(stack *internal.RegionStack) string {
		if stack == nil {
			return "<nil>"
		}
		name := rootFunc[stack.Start.G]
		end := ""
		if stack.Local != nil {
			name = stack.Local.Kind
			end = fmt.Sprintf("-%d", stack.Local.Events[len(stack.Local.Events)-1].Ts)
		}
		return fmt.Sprintf("%s(g=%d@%d%s)", name, stack.Start.G, stack.Start.Ts, end)
	}

	if *showChains {
		for stack := range firstSaw {
			var line []string
			for try := stack; try != nil; try = try.Parent {
				line = append(line, stackString(try))
			}
			for i, j := 0, len(line)-1; i < j; i, j = i+1, j-1 {
				line[i], line[j] = line[j], line[i]
			}
			fmt.Printf("%s\n", strings.Join(line, " "))
		}
		return
	}

	if *showPairs {
		pairs := make(map[[2]*internal.RegionStack]struct{})
		for stack := range firstSaw {
			var prev *internal.RegionStack
			for try := stack; try != nil; try = try.Parent {
				if prev != nil {
					pairs[[2]*internal.RegionStack{try, prev}] = struct{}{}
				}
				prev = try
			}
			pairs[[2]*internal.RegionStack{nil, prev}] = struct{}{}
		}
		for pair := range pairs {
			fmt.Printf("%s %s\n", stackString(pair[0]), stackString(pair[1]))
		}
		return
	}

	if *showRegions {
		for _, reg := range regions {
			fmt.Printf("%s(g=%d@%d-%d)\n", reg.Kind, reg.Events[0].G, reg.Events[0].Ts, reg.Events[len(reg.Events)-1].Ts)
		}
		return
	}

	if *showJSON {
		spans := cluster.ExtractSpans(data, pattern.TrackAll)

		for _, span := range spans {
			var v interface{} = span
			if *summarize {
				v = cluster.Summarize(span)
			}

			buf, err := json.Marshal(v)
			if err != nil {
				log.Fatalf("json.Marshal: %v", err)
			}
			fmt.Printf("%s\n", buf)
		}
	}
}
