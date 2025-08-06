package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"hash"
	"io"
	"log"
	"os"
	"strings"

	"github.com/google/pprof/profile"
	"golang.org/x/exp/trace"
)

func main() {
	input := flag.String("input", "", "Path to execution trace file")
	profFile := flag.String("profile", "", "Output path for CPU profile covering STW ranges")
	printSTW := flag.Bool("print-stw", true, "Log a summary of each STW range")
	printStacks := flag.Bool("print-stacks", false, "Log each CPU sample that arrived during a STW range")
	grace := flag.Duration("grace", 0, "Grace period, to ignore samples that arrive early in each STW phase")
	flag.Parse()

	f, err := os.Open(*input)
	if err != nil {
		log.Fatalf("os.Open: %v", err)
	}
	defer f.Close()

	hash := sha256.New()
	var rd io.Reader = bufio.NewReader(&hashReader{
		Hash:   hash,
		Reader: f,
	})

	reader, err := trace.NewReader(rd)
	if err != nil {
		log.Fatalf("trace.NewReader: %v", err)
	}

	var pb profileBuilder
	pb.profile.PeriodType = &profile.ValueType{Type: "cpu", Unit: "nanoseconds"}
	pb.profile.Period = 1

	pb.profile.SampleType = []*profile.ValueType{
		{Type: "samples", Unit: "count"},
		{Type: "offset", Unit: "nanoseconds"},
	}
	pb.profile.DefaultSampleType = "samples"

	var (
		procActive = make(map[trace.ProcID]trace.ProcState)
		procs      = make(map[trace.ProcID]trace.ProcState)

		// The execution trace includes explicit signaling of the start and end
		// of the STW request. The start and end of the critical section when
		// the world is fully stopped are implicit. We look for times when
		// there's only one P in an "executing" state, followed by either the
		// explicit end event or a P transitioning into an "executing" state.
		//
		// We may not know about all of the Ps initially (at the start of the
		// trace), so we may see several Proc StateTransition events that leave
		// us with one known "executing" P.
		//
		// We may also see Ps transition into the "executing" state (perhaps
		// early in the process of stopping the world) before the critical
		// section begins. And after the critical section ends, we may also of
		// course see Ps that start and then stop.

		stwRequest trace.Event // start of a STW region
		stwIdle    trace.Event // all (known) Ps have settled, may be updated
		stwResume  trace.Event // end of critical section, after getting to idle

		stwStackSample []trace.Event

		first trace.Event
	)

	for {
		ev, err := reader.ReadEvent()
		if err == io.EOF {
			break
		} else if err != nil {
			log.Fatalf("trace.Reader.ReadEvent: %v", err)
		}
		if first.Kind() == 0 {
			first = ev
		}

		switch ev.Kind() {
		case trace.EventStateTransition:
			st := ev.StateTransition()
			switch st.Resource.Kind {
			case trace.ResourceProc:
				p := st.Resource.Proc()
				_, to := st.Proc()
				procs[p] = to
				if to.Executing() {
					procActive[p] = to
				} else {
					delete(procActive, p)
				}

				if stwRequest.Kind() != 0 && stwResume.Kind() == 0 && to.Executing() == false && len(procActive) == 1 {
					// best guess at this time
					stwIdle = ev
				}
				if stwRequest.Kind() != 0 && stwIdle.Kind() != 0 && stwResume.Kind() == 0 && to.Executing() == true {
					// world seems to be restarting, critical section must be done
					stwResume = ev
				}
			}
		case trace.EventRangeBegin:
			if isSTW(ev) {
				if stwRequest.Kind() != 0 {
					log.Fatalf("STW begins while STW is active:\nhad %v\ngot %v\n", stwRequest, ev)
				}
				stwRequest = ev
				if len(procActive) == 1 {
					stwIdle = ev // best guess at this time
				}
			}
		case trace.EventRangeEnd:
			if isSTW(ev) {
				if stwResume.Kind() == 0 {
					stwResume = ev
				}

				if *printSTW {
					fmt.Printf("STW @%0.3fs: %.3g+%.3g+%.3g ms clock, %d samples\n",
						stwRequest.Time().Sub(first.Time()).Seconds(),
						float64(stwIdle.Time().Sub(stwRequest.Time()).Microseconds())/1000,
						float64(stwResume.Time().Sub(stwIdle.Time()).Microseconds())/1000,
						float64(ev.Time().Sub(stwResume.Time()).Microseconds())/1000,
						len(stwStackSample),
					)
				}

				processSTWPhase := func(start, end trace.Event, desc string) {
					for _, ev := range stwStackSample {
						if ev.Time().Sub(start.Time()) < 0 {
							continue
						}
						if ev.Time().Sub(end.Time()) >= 0 {
							return
						}

						offset := ev.Time().Sub(start.Time()).Nanoseconds()
						if offset < int64(*grace) {
							continue
						}

						if *printStacks {
							fmt.Printf("%s +%dµs g=%d\n", desc, ev.Time().Sub(start.Time()).Microseconds(), ev.Goroutine())
						}
						s := &profile.Sample{
							Value: []int64{1, offset},
							Label: map[string][]string{
								"stw-phase": {desc},
							},
							NumLabel: map[string][]int64{
								"g":      {int64(ev.Goroutine())},
								"offset": {offset},
							},
							NumUnit: map[string][]string{
								"offset": {"ns"},
							},
						}
						for f := range ev.Stack().Frames() {
							if *printStacks {
								fmt.Printf("  %s %s:%d %#x\n", f.Func, f.File, f.Line, f.PC)
							}
							s.Location = append(s.Location, pb.getLocation(f))
						}
						pb.profile.Sample = append(pb.profile.Sample, s)
					}
				}

				processSTWPhase(stwRequest, stwIdle, "pre-stop")
				processSTWPhase(stwIdle, stwResume, "stopped")
				processSTWPhase(stwResume, ev, "post-stop")

				stwRequest = trace.Event{}
				stwIdle = trace.Event{}
				stwResume = trace.Event{}
				stwStackSample = nil
			}
		case trace.EventStackSample:
			if stwRequest.Kind() != 0 {
				stwStackSample = append(stwStackSample, ev)
			}
		}
	}

	if *profFile != "" {
		prof := pb.profile.Compact()
		prof.SetLabel("trace-sha256", []string{fmt.Sprintf("%02x", hash.Sum(nil))})
		buf := new(bytes.Buffer)
		err = prof.Write(buf)
		if err != nil {
			log.Fatalf("format profile: %v", err)
		}
		err = os.WriteFile(*profFile, buf.Bytes(), 0644)
		if err != nil {
			log.Fatalf("write profile: %v", err)
		}
	}
}

func isSTW(ev trace.Event) bool {
	switch ev.Kind() {
	default:
		return false
	case trace.EventRangeBegin, trace.EventRangeActive, trace.EventRangeEnd:
		prefix, _, _ := strings.Cut(ev.Range().Name, " ")
		return prefix == "stop-the-world"
	}
}

type hashReader struct {
	Hash   hash.Hash
	Reader io.Reader
}

func (hr *hashReader) Read(p []byte) (int, error) {
	nr, err := hr.Reader.Read(p)
	_, hashErr := hr.Hash.Write(p[:nr])

	if err != nil {
		return nr, err
	}

	return nr, hashErr
}

type profileBuilder struct {
	profile   profile.Profile
	mapping   profile.Mapping
	functions map[[2]string]*profile.Function
	locations map[uint64]*profile.Location
}

func (pb *profileBuilder) init() {
	if pb.functions != nil {
		return
	}
	pb.mapping.ID = 1
	pb.mapping.HasFilenames = true
	pb.mapping.HasFunctions = true
	pb.mapping.HasLineNumbers = true
	pb.profile.Mapping = append(pb.profile.Mapping, &pb.mapping)
	pb.functions = make(map[[2]string]*profile.Function)
	pb.locations = make(map[uint64]*profile.Location)

}

func (pb *profileBuilder) getFunction(f trace.StackFrame) *profile.Function {
	pb.init()
	key := [2]string{f.Func, f.File}
	fn, ok := pb.functions[key]
	if !ok {
		fn = &profile.Function{
			Name:       f.Func,
			SystemName: f.Func,
			Filename:   f.File,
			ID:         uint64(len(pb.functions) + 1),
		}
		pb.functions[key] = fn
		pb.profile.Function = append(pb.profile.Function, fn)
	}
	return fn
}

func (pb *profileBuilder) getLocation(f trace.StackFrame) *profile.Location {
	pb.init()
	l, ok := pb.locations[f.PC]
	if !ok {
		l = &profile.Location{
			Address: f.PC,
			Line: []profile.Line{{
				Function: pb.getFunction(f),
				Line:     int64(f.Line),
			}},
			Mapping: &pb.mapping,
			ID:      uint64(len(pb.locations) + 1),
		}
		pb.locations[f.PC] = l
		pb.profile.Location = append(pb.profile.Location, l)
	}
	return l
}
