package cluster_test

import (
	"reflect"
	"testing"

	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
	"github.com/rhysh/go-tracing-toolbox/internal/cluster"
	"github.com/rhysh/go-tracing-toolbox/internal/pattern"
	"github.com/rhysh/go-tracing-toolbox/internal/testhelp"
)

func TestRunning(t *testing.T) {
	testcase := func(span *cluster.Span, run, net [][2]int64) func(t *testing.T) {
		return func(t *testing.T) {
			ranges, err := cluster.Running(span)
			if err != nil {
				t.Fatalf("cluster.Running; err = %q", err)
			}
			r, n := ranges.Running, ranges.Waiting["net"]
			if have, want := r, run; !reflect.DeepEqual(have, want) {
				t.Errorf("Running; %v != %v", have, want)
			}
			if have, want := n, net; !reflect.DeepEqual(have, want) {
				t.Errorf("Network; %v != %v", have, want)
			}
		}
	}

	t.Run("", testcase(&cluster.Span{
		StartNs:   1000,
		LengthNs:  50,
		StartRun:  []int64{10, 40, 49},
		StartWait: map[string][]int64{"net": {35}, "cpu": {42}},
	}, [][2]int64{
		{10, 35}, {40, 42}, {49, 50},
	}, [][2]int64{
		{35, 40},
	}))
}

func TestMore(t *testing.T) {
	data := testhelp.Load(t, "../../testdata/f2f5b4bd_go1.18.7/gc_assist")

	spans := cluster.ExtractSpans(data, pattern.TrackAll)

	var root *cluster.Span
	for _, span := range spans {
		if span.G == 130809 && span.StartNs == 1185138955 && span.Kind == "server/http" {
			root = span
			break
		}
	}
	if root == nil {
		t.Fatalf("Could not find root span")
	}

	summary := cluster.Summarize(root)

	if have, want := summary.LengthNs, int64(692_429_419); have != want {
		t.Errorf("LengthNs; %d != %d", have, want)
	}
	if have, want := summary.TotalRunNs, int64(259_365_452); have != want {
		t.Errorf("TotalRunNs; %d != %d", have, want)
	}
	if have, want := summary.FlatRunNs, int64(258_785_617); have != want {
		t.Errorf("FlatRunNs; %d != %d", have, want)
	}

	if have, want := summary.TotalAssistNs, map[string]int64{
		"gc": 234_573_256,
	}; !reflect.DeepEqual(have, want) {
		t.Errorf("TotalAssistNs; %v != %v", have, want)
	}
	if have, want := summary.FlatAssistNs, map[string]int64{
		"gc": 234_095_265,
	}; !reflect.DeepEqual(have, want) {
		t.Errorf("FlatAssistNs; %v != %v", have, want)
	}
	if have, want := summary.FlatWaitNs, map[string]int64{
		"cpu": 349_195_308,
		"net": 84_448_494,
	}; !reflect.DeepEqual(have, want) {
		t.Errorf("FlatWaitNs; %v != %v", have, want)
	}

	if have, want := summary.Root, root; have != want {
		t.Errorf("Root; %v != %v", have, want)
	}
}

func TestManualA(t *testing.T) {
	data := testhelp.Load(t, "../../testdata/manual/a")

	spans := cluster.ExtractSpans(data, func(evs []*trace.Event) []*internal.Region {
		return []*internal.Region{{Events: evs, Kind: "goroutine"}}
	})

	var root *cluster.Span
	for _, span := range spans {
		if span.G == 10 && span.StartNs == 4000 && span.Kind == "goroutine" {
			root = span
			break
		}
	}
	if root == nil {
		t.Fatalf("Could not find root span")
	}

	summary := cluster.Summarize(root)

	// The root is on g10, which starts at 4000 and ends at 81300
	if have, want := summary.LengthNs, int64(81300-4000); have != want {
		t.Errorf("LengthNs; %d != %d", have, want)
	}
	// In the window between 4000 and 81300:
	//  - g10 runs from 4000 to 4300, a length of 300
	//  - g20 runs from 40000 to 44000, a length of 4000 (but isn't caused by g10)
	//  - g30 runs from 72000 to 78000, a length of 6000 (but isn't caused by g10)
	//  - g40 runs from 4500 to 20000, a length of 15500
	//  - g40 runs from 57000 to 61000, a length of 4000
	//  - g40 runs from 79000 to 82000, which gets trimmed at 81300 to be a length of 2300
	if have, want := summary.TotalRunNs, int64(300+15500+4000+2300); have != want {
		t.Errorf("TotalRunNs; %d != %d", have, want)
	}
	// In the window between 4000 and 81300:
	//  - g10 runs from 4000 to 4300, a length of 300
	//  - g40 runs from 4500 to 20000, a length of 15500
	//  - g40 runs from 57000 to 61000, a length of 4000
	//  - g40 runs from 79000 to 82000, which gets trimmed at 81300 to be a length of 2300
	if have, want := summary.FlatRunNs, int64(300+15500+4000+2300); have != want {
		t.Errorf("FlatRunNs; %d != %d", have, want)
	}

	if have, want := summary.TotalAssistNs, map[string]int64{}; !reflect.DeepEqual(have, want) {
		t.Errorf("TotalAssistNs; %v != %v", have, want)
	}
	if have, want := summary.FlatAssistNs, map[string]int64{}; !reflect.DeepEqual(have, want) {
		t.Errorf("FlatAssistNs; %v != %v", have, want)
	}
	// In the window between 4000 and 81300:
	//  - g40 is in "cpu" from 4000 to 4500, but g10 ran until 4300, so we count only 200
	//  - g40 is in "cpu" from 40000 to 57000, a length of 17000
	//  - g40 is in "cpu" from 72000 to 79000, a length of 7000
	//  - g40 is in "select" from 20000 to 40000, a length of 20000
	//  - g40 is in "send" from 61000 to 72000, a length of 11000
	//  - g10 is in "block" from 6000 to 81300, but "block" is shadowed by most waits and all on-CPU time,
	//    so we only count from 82000 to ???
	if have, want := summary.FlatWaitNs, map[string]int64{
		"cpu":    200 + 17000 + 7000,
		"select": 20000,
		"send":   11000,
		// "block":  1,
	}; !reflect.DeepEqual(have, want) {
		t.Errorf("FlatWaitNs; %v != %v", have, want)
	}

	if have, want := summary.Root, root; have != want {
		t.Errorf("Root; %v != %v", have, want)
	}
}
