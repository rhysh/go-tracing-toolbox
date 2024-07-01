// The grstates command shows the goroutine state machines from a program's execution trace
//
// An execution trace, collected with the runtime/trace package, describes all
// of the times a program's goroutines interacted with the scheduler: when they
// make syscalls (to read or write network data), when they launch or unblock
// other goroutines, when they need to wait on a channel or a lock.
//
// By watching a goroutine throughout its lifecycle as the functions on its call
// stack change, we can build a view of its actions as a state machine.
//
// This tool processes an execution trace into a directed graph in the DOT
// language and rendered as an SVG, via the same "dot" tool that "go tool pprof"
// uses.
package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"

	driver "github.com/rhysh/go-tracing-toolbox/cmd/grstates/internal/pprof_driver"
	"golang.org/x/exp/trace"
)

func main() {
	input := flag.String("input", "", "Path to execution trace file")
	dotFile := flag.String("dot", "", "Path to DOT-format directed graph output")
	svgFile := flag.String("svg", "", "Path to SVG-format image output")
	flag.Parse()

	f, err := os.Open(*input)
	if err != nil {
		log.Fatalf("os.Open: %v", err)
	}
	defer f.Close()
	reader, err := trace.NewReader(bufio.NewReader(f))
	if err != nil {
		log.Fatalf("trace.NewReader: %v", err)
	}

	var (
		goroutines = make(map[trace.GoID]*behaviors)
		stackSet   = newStackSet()
		why        = newExamples()
	)

	for {
		ev, err := reader.ReadEvent()
		if err == io.EOF {
			break
		} else if err != nil {
			log.Fatalf("trace.Reader.ReadEvent: %v", err)
		}

		switch ev.Kind() {
		case trace.EventStateTransition:
			src := ev.Goroutine()
			dst := trace.NoGoroutine

			srcStk := stackSet.canonical(ev.Stack())
			dstStk := trace.NoStack

			ext := ev.StateTransition()
			switch ext.Resource.Kind {
			case trace.ResourceGoroutine:
				dst = ext.Resource.Goroutine()
				dstStk = stackSet.canonical(ext.Stack)

				gstEvent := goroutineStateTransition(ev)

				if src != dst {
					if src != trace.NoGoroutine {
						getBehaviors(goroutines, why, src).transitionOrigin(gstEvent, srcStk)
					}
					if dst != trace.NoGoroutine {
						getBehaviors(goroutines, why, dst).transitionTarget(gstEvent, dstStk)
					}
				} else if src != trace.NoGoroutine {
					getBehaviors(goroutines, why, src).transitionTarget(gstEvent, srcStk)
				}
			}
		}
	}

	if *dotFile != "" {
		dotBuf := new(bytes.Buffer)
		err := writeDot(dotBuf, goroutines, why)
		if err != nil {
			log.Fatalf("generate dot file: %v", err)
		}
		err = os.WriteFile(*dotFile, dotBuf.Bytes(), 0700)
		if err != nil {
			log.Fatalf("write dot file: %v", err)
		}
	}
	if *svgFile != "" {
		var dotBuf, svgBuf, errBuf bytes.Buffer
		err := writeDot(&dotBuf, goroutines, why)
		if err != nil {
			log.Fatalf("generate dot file: %v", err)
		}
		ctx := context.Background()
		cmd := exec.CommandContext(ctx, "dot", "-T", "svg")
		cmd.Stdin = &dotBuf
		cmd.Stdout = &svgBuf
		cmd.Stderr = &errBuf
		err = cmd.Run()
		if err != nil {
			log.Fatalf("generate svg file: %v\n%s", err, errBuf.String())
		}
		svg := driver.MassageSVG(svgBuf.String())
		err = os.WriteFile(*svgFile, []byte(svg), 0700)
		if err != nil {
			log.Fatalf("write svg file: %v", err)
		}
	}
}

type stackSet struct {
	stackStrings map[trace.Stack]string
	stringStacks map[string]trace.Stack
}

func newStackSet() *stackSet {
	return &stackSet{
		stackStrings: make(map[trace.Stack]string),
		stringStacks: make(map[string]trace.Stack),
	}
}

func (ss *stackSet) canonical(stk trace.Stack) trace.Stack {
	return ss.stringStacks[ss.format(stk)]
}

func (ss *stackSet) format(stk trace.Stack) string {
	have, ok := ss.stackStrings[stk]
	if ok {
		return have
	}

	buf := new(strings.Builder)
	stk.Frames(func(f trace.StackFrame) bool {
		fmt.Fprintf(buf, "%s@%#x %s:%d\n", f.Func, f.PC, f.File, f.Line)
		return true
	})
	str := buf.String()
	ss.stackStrings[stk] = str

	if _, ok := ss.stringStacks[str]; !ok {
		ss.stringStacks[str] = stk
	}

	return str
}

func (ss *stackSet) formatShort(stk trace.Stack) string {
	var frames []trace.StackFrame
	stk.Frames(func(f trace.StackFrame) bool {
		frames = append(frames, f)
		return true
	})
	buf := new(strings.Builder)
	for i := len(frames) - 1; i >= 0; i-- {
		fmt.Fprintf(buf, "%s:%d\n", frames[i].Func, frames[i].Line)
	}
	return buf.String()
}

func getBehaviors(goroutines map[trace.GoID]*behaviors, why *examples, goid trace.GoID) *behaviors {
	b, ok := goroutines[goid]
	if !ok {
		b = &behaviors{
			why:   why,
			edges: make(map[edge]int),
		}
		goroutines[goid] = b
	}
	return b
}

type stackState struct {
	stack trace.Stack
	state trace.GoState
}

type edge struct {
	from stackState
	to   stackState
	via  [8]trace.GoState
}

type behaviors struct {
	why *examples

	edges map[edge]int

	prevState stackState
	via       [8]trace.GoState
	current   trace.GoState
	preempted bool
}

// transitionOrigin processes a trace.Event that describes this goroutine
// effecting a StateTransition.
func (b *behaviors) transitionOrigin(ev goroutineStateTransition, stk trace.Stack) {
	if b == nil {
		return
	}

	if b.current == trace.GoUndetermined {
		// If we don't yet know this goroutine's state, but it's causing other
		// goroutines to change states .. then it must be Running.
		b.current = trace.GoRunning
	}

	if stk == trace.NoStack {
		// An interesting stack is the only reason to be here (since in this
		// context, we have no new state)
		return
	}

	b.notice(ev, stk, b.current)

	if stk != trace.NoStack {
		// These stacks aren't a reliable depiction of the goroutine state machine
		if !(ev.isAsyncPreemption() || stackIsMalloc(stk) || stackIsBuggy(stk)) {
			b.prevState = stackState{stack: stk, state: b.current}
		}
	}
}

// transitionTarget processes a trace.Event that describes a StateTransition
// affecting this goroutine.
func (b *behaviors) transitionTarget(ev goroutineStateTransition, stk trace.Stack) {
	if b == nil {
		return
	}
	from, to := trace.Event(ev).StateTransition().Goroutine()
	if from == trace.GoNotExist {
		// Use the canonical version, stk.
		b.prevState = stackState{stack: stk, state: from}
	}

	b.notice(ev, stk, to)
	b.current = to
}

func (b *behaviors) notice(ev goroutineStateTransition, stk trace.Stack, to trace.GoState) {
	// Ignore (pairs of) preemption events, including Gosched
	if b.preempted {
		if to == trace.GoRunning {
			return
		}
		b.preempted = false
	}
	if ev.isPreemption() {
		b.preempted = true
	}

	// These stacks aren't a reliable depiction of the goroutine state machine
	if ev.isAsyncPreemption() || stackIsMalloc(stk) || stackIsBuggy(stk) {
		return
	}

	if stk == trace.NoStack && to != trace.GoNotExist {
		for i := len(b.via) - 1; i > 0; i-- {
			b.via[i] = b.via[i-1]
		}
		b.via[0] = to
		return
	}

	nextState := stackState{
		stack: stk,
		state: to,
	}
	edge := edge{from: b.prevState, to: nextState, via: b.via}
	b.edges[edge]++
	b.why.offerEdgeTo(edge, trace.Event(ev))
	b.why.offerStackState(nextState, trace.Event(ev))
	b.prevState = nextState
	for i := range b.via {
		b.via[i] = trace.GoUndetermined
	}
}

type goroutineStateTransition trace.Event

func (ev goroutineStateTransition) isGoroutineCreation() bool {
	from, _ := trace.Event(ev).StateTransition().Goroutine()
	return from == trace.GoNotExist
}

func (ev goroutineStateTransition) isPreemption() bool {
	from, to := trace.Event(ev).StateTransition().Goroutine()
	return from == trace.GoRunning && to == trace.GoRunnable
}

func (ev goroutineStateTransition) isAsyncPreemption() bool {
	if !ev.isPreemption() {
		return false
	}

	hasGosched := false
	trace.Event(ev).Stack().Frames(func(f trace.StackFrame) bool {
		if f.Func == "runtime.Gosched" {
			hasGosched = true
		}
		return true
	})
	return !hasGosched
}

func stackIsBuggy(stk trace.Stack) bool {
	bug := false
	stk.Frames(func(f trace.StackFrame) bool {
		if f.Line == 0 {
			// There should be a real stack for this event, but it's obscured by
			// https://go.dev/issue/68090
			bug = true
		}
		return true
	})
	return bug
}

func stackIsMalloc(stk trace.Stack) bool {
	malloc := false
	stk.Frames(func(f trace.StackFrame) bool {
		if f.Func == "runtime.mallocgc" {
			// When mallocgc is on the stack, the event likely describes GC
			// Assist work. In that case, the event doesn't represent an
			// inter-goroutine synchronization point, and we're not going to
			// consistently observe this same stack in subsequent runs of this
			// goroutine's state machine. Ignore it.
			malloc = true
		}
		return true
	})
	return malloc
}

type examples struct {
	stackState map[stackState]trace.Event
	edgeTo     map[edge]trace.Event
}

func newExamples() *examples {
	return &examples{
		stackState: make(map[stackState]trace.Event),
		edgeTo:     make(map[edge]trace.Event),
	}
}

func (e *examples) offerStackState(key stackState, ev trace.Event) {
	prev, ok := e.stackState[key]
	if ok && ev.Time().Sub(prev.Time()) > 0 {
		return
	}
	e.stackState[key] = ev
}

func (e *examples) offerEdgeTo(key edge, ev trace.Event) {
	prev, ok := e.edgeTo[key]
	if ok && ev.Time().Sub(prev.Time()) > 0 {
		return
	}
	e.edgeTo[key] = ev
}
