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
			if goid := ev.Goroutine(); goid != trace.NoGoroutine && ev.Stack() != trace.NoStack {
				stk := stackSet.canonical(ev.Stack())
				b := getBehaviors(goroutines, goid)
				b.transitionOrigin(ev, stk)
			}

			ext := ev.StateTransition()
			switch ext.Resource.Kind {
			case trace.ResourceGoroutine:
				if goid := ext.Resource.Goroutine(); goid != trace.NoGoroutine {
					b := getBehaviors(goroutines, goid)
					if ext.Stack != trace.NoStack {
						stk := stackSet.canonical(ext.Stack)
						b.transitionTarget(ev, stk)
					}
					if _, to := ext.Goroutine(); to == trace.GoNotExist {
						b.exit(ev)
					}
				}
			}
		}
	}

	if *dotFile != "" {
		dotBuf := new(bytes.Buffer)
		err := writeDot(dotBuf, goroutines)
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
		err := writeDot(&dotBuf, goroutines)
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

func getBehaviors(goroutines map[trace.GoID]*behaviors, goid trace.GoID) *behaviors {
	b, ok := goroutines[goid]
	if !ok {
		b = &behaviors{
			pairs: make(map[[2]trace.Stack]int),
		}
		goroutines[goid] = b
	}
	return b
}

type behaviors struct {
	pairs map[[2]trace.Stack]int

	lastEv trace.Event

	initStack trace.Stack
	prevStack trace.Stack
	exited    bool
}

func (b *behaviors) exit(ev trace.Event) {
	b.exited = true
}

func (b *behaviors) transitionOrigin(ev trace.Event, stk trace.Stack) {
	b.notice(ev, stk)
}

func (b *behaviors) transitionTarget(ev trace.Event, stk trace.Stack) {
	b.notice(ev, stk)
	if isGoroutineCreation(ev) {
		// Use the canonical version, stk.
		b.initStack = stk
	}
}

func (b *behaviors) notice(ev trace.Event, stk trace.Stack) {
	novel := false
	if ev != b.lastEv {
		novel = true
		b.lastEv = ev
	}

	if !novel {
		return
	}

	if isAsyncPreemption(ev) || stackIsMalloc(stk) || stackIsBuggy(stk) {
		return
	}

	b.pairs[[2]trace.Stack{b.prevStack, stk}]++
	b.prevStack = stk
}

func isGoroutineCreation(ev trace.Event) bool {
	if ev.Kind() != trace.EventStateTransition {
		return false
	}
	st := ev.StateTransition()
	if st.Resource.Kind != trace.ResourceGoroutine {
		return false
	}
	if from, _ := st.Goroutine(); from != trace.GoNotExist {
		return false
	}
	return true
}

func isAsyncPreemption(ev trace.Event) bool {
	if ev.Kind() != trace.EventStateTransition {
		return false
	}
	st := ev.StateTransition()
	if st.Resource.Kind != trace.ResourceGoroutine {
		return false
	}
	if ev.Goroutine() != st.Resource.Goroutine() {
		return false
	}
	if from, to := st.Goroutine(); !(from == trace.GoRunning && to == trace.GoRunnable) {
		return false
	}

	hasGosched := false
	ev.Stack().Frames(func(f trace.StackFrame) bool {
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
