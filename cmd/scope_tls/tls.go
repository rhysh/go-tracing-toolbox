package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/pprof/profile"
	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func main() {
	input := flag.String("input", "", "Path to execution trace file")
	prof := flag.String("profile", "", "Path to corresponding CPU profile (optional)")
	output := flag.String("output", "", "Path to output profile")
	text := flag.Bool("text", false, "Write text format to stdout")
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
		log.Fatalf("trace.Parse; err = %v", err)
	}

	outProfile := new(profile.Profile)
	outProfile.SampleType = append(outProfile.SampleType,
		&profile.ValueType{Type: "samples", Unit: "count"},
		&profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
	)
	outProfile.Period = (10 * time.Millisecond).Nanoseconds()
	outProfile.PeriodType = outProfile.SampleType[1]
	outProfile.DurationNanos = data.Events[len(data.Events)-1].Ts - data.Events[0].Ts

	// PeriodType: cpu nanoseconds
	// Period: 10000000
	// Time: 2022-09-15 13:19:01.577071878 -0700 PDT
	// Duration: 1.20
	// Samples:
	// samples/count cpu/nanoseconds

	var pprof *profile.Profile
	if *prof != "" {
		buf, err := os.ReadFile(*prof)
		if err != nil {
			log.Fatalf("ReadFile; err = %v", err)
		}
		pprof, err = profile.Parse(bytes.NewReader(buf))
		if err != nil {
			log.Fatalf("profile.Parse; err = %v", err)
		}

		// grab inlining and file mapping info, maybe it's useful
		outProfile = pprof.Copy()
		outProfile.FilterSamplesByName(nil, regexp.MustCompile(`.*`), nil, nil)

		// TODO: maybe there's a way to see that two samples (as seen in the
		// profile, and in the trace) are the same without confusing
		// nearly-identical twins (which might have different labels)
	}

	locFromPC := make(map[uint64]*profile.Location)
	fnFromName := make(map[string]*profile.Function)

	for _, g := range data.GoroutineList {
		gevs := data.GoroutineEvents[g]
		var (
			left  *trace.Event
			right *trace.Event
		)

		for _, ev := range gevs {
			if ev.Type == trace.EvGoBlockNet && internal.HasStackRe(ev.Stk,
				"**", "^crypto/tls...Conn..readHandshake$",
				"**", "^net...conn..Read$",
				"**") {
				left = ev
			}
			if right == nil &&
				(ev.Type == trace.EvGoSysCall || ev.Type == trace.EvGoBlockNet) &&
				internal.HasStackRe(ev.Stk,
					"**", "^crypto/tls...clientHandshakeState..readFinished$",
					"**", "^net...conn..Read$",
					"**") {
				right = ev
			}
		}
		if left != nil && right != nil {
			left = left.Link
			for _, ev := range gevs {
				if ev.Ts >= left.Ts && ev.Ts < right.Ts {
					if ev.Type == trace.EvCPUSample {
						frac := float64(ev.Ts-left.Ts) / float64(right.Ts-left.Ts)
						var stack []string
						for _, frame := range ev.Stk {
							stack = append(stack, frame.Fn)
						}
						for i, j := 0, len(stack)-1; i < j; i, j = i+1, j-1 {
							stack[i], stack[j] = stack[j], stack[i]
						}
						if *text {
							fmt.Printf("start %d length %d frac %f stack %s\n",
								left.Ts, right.Ts-left.Ts, frac, strings.Join(stack, ";"))
						}

						var sam1 profile.Sample
						sam1.Value = append(sam1.Value, 1, outProfile.Period)
						sam1.NumLabel = make(map[string][]int64)
						sam1.NumUnit = make(map[string][]string)

						sam1.NumLabel["region_start"] = []int64{left.Ts}
						sam1.NumLabel["region_length"] = []int64{right.Ts - left.Ts}
						sam1.NumLabel["sample_offset"] = []int64{ev.Ts - left.Ts,
							int64(100 * float64(ev.Ts-left.Ts) / float64(right.Ts-left.Ts))}
						sam1.NumUnit["region_start"] = []string{"nanoseconds"}
						sam1.NumUnit["region_length"] = []string{"nanoseconds"}
						sam1.NumUnit["sample_offset"] = []string{"nanoseconds", "percent"}

						for i := len(ev.Stk) - 1; i >= 0; i-- {
							frame := ev.Stk[i]

							fn, ok := fnFromName[frame.Fn]
							if !ok {
								fn = new(profile.Function)
								fn.ID = uint64(len(outProfile.Function) + 1)
								fn.Name = frame.Fn
								fn.Filename = frame.File

								fnFromName[frame.Fn] = fn
								outProfile.Function = append(outProfile.Function, fn)
							}

							loc, ok := locFromPC[frame.PC]
							if !ok {
								loc = new(profile.Location)
								loc.ID = uint64(len(outProfile.Location) + 1)
								loc.Address = frame.PC
								loc.Line = append(loc.Line, profile.Line{Function: fn, Line: int64(frame.Line)})

								locFromPC[frame.PC] = loc
								outProfile.Location = append(outProfile.Location, loc)
							}

							sam1.Location = append(sam1.Location, loc)
						}
						outProfile.Sample = append(outProfile.Sample, &sam1)
					}
				}
			}
		}
	}

	outProfile.Compact()

	if *output != "" {
		var outBuf bytes.Buffer
		err = outProfile.Write(&outBuf)
		if err != nil {
			log.Fatalf("Write; err = %v", err)
		}
		err = os.WriteFile(*output, outBuf.Bytes(), 0600)
		if err != nil {
			log.Fatalf("WriteFile; err = %v", err)
		}
	}
}
