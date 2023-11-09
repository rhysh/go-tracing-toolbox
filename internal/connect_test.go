package internal_test

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
	"github.com/rhysh/go-tracing-toolbox/internal/pattern"
	"github.com/rhysh/go-tracing-toolbox/internal/testhelp"
)

func TestTasks(t *testing.T) {
	// Here's an opportunity to add logging to or alter the connection rules.
	rc := &internal.RegionConnector{
		StartRegion: func(ev *trace.Event, existing, starting *internal.RegionStack) *internal.RegionStack {
			return (*internal.RegionConnector)(nil).DoStartRegion(ev, existing, starting)
		},
		ApplyOnWake: func(ev *trace.Event, existing, inbound *internal.RegionStack) *internal.RegionStack {
			return (*internal.RegionConnector)(nil).DoApplyOnWake(ev, existing, inbound)
		},
	}

	t.Run("point overlap", func(t *testing.T) {
		data := testhelp.Load(t, "../testdata/7de55ef9_go1.15.10/http_conn_serve")
		regions := testhelp.FindAll(data, pattern.TrackAll)
		// synthesize the region we'd find with SDK detection
		for i, evs := 0, data.GoroutineEvents[680534068]; i < len(evs); i++ {
			if evs[i].Ts == 964255756 {
				regions = append(regions, &internal.Region{Kind: "server/sdk", Events: evs[i : i+3]})
				break
			}
		}
		out := rc.Process(&internal.ProcessRegionConnectionsInput{
			Data:    data,
			Regions: regions,
		})
		evNames := make(map[int64][]string)
		for _, ev := range data.Events {
			var names []string
			for stack := out.EventRegionStacks[ev]; stack != nil; stack = stack.Parent {
				if stack.Local != nil {
					names = append(names, stack.Local.Kind)
				}
			}
			evNames[ev.Ts] = names
		}
		need := map[int64][]string{
			964255756: {
				"server/sdk",
				"server/http",
			},
		}
		for ts, want := range need {
			have, ok := evNames[ts]
			if !ok {
				t.Errorf("no region info for timestamp %d; wanted %q", ts, want)
				continue
			}
			if !reflect.DeepEqual(have, want) {
				t.Errorf("incorrect region info for timestamp %d; %q != %q", ts, have, want)
			}
		}
	})

	t.Run("been there", func(t *testing.T) {
		data := testhelp.Load(t, "../testdata/7de55ef9_go1.15.10/dial_own_tls_with_shared_dns")

		var regions []*internal.Region
		for _, fn := range []func([]*trace.Event) []*internal.Region{
			pattern.TrackHTTP1Writer,
			pattern.TrackHTTP1Reader,
			pattern.TrackHTTPClient,
		} {
			regions = append(regions, testhelp.FindAll(data, fn)...)
		}
		out := rc.Process(&internal.ProcessRegionConnectionsInput{
			Data:    data,
			Regions: regions,
		})

		needBeenThere := map[int64][]uint64{
			// 680530334 "net/http.(*persistConn).readLoop"
			702438203: {
				680531181,
				680531183,
				680530334,
			},
			// 680530335 "net/http.(*persistConn).writeLoop"
			702443557: {
				680531181,
				680531183,
				680530335,
			},
			// 680531181 RoundTrip
			695730843: {680531181},
			727242718: nil,
			// 680531183 "net/http.(*Transport).dialConnFor"
			695748081: {680531181, 680531183},

			// 680531184 "internal/singleflight.(*Group).doCall"
			695769137: {
				680531181,
				680531183,
				680531184,
			},

			// 680533121 "net.(*Resolver).goLookupIPCNAMEOrder.func3.1"
			695829041: {
				680531181,
				680531183,
				680531184,
				680533121,
			},

			// 680531392 RoundTrip
			695888518: {
				680531392,
			},
			745998692: { // BUG
				680531181,
				680531183,
				680531184,
				680533090,
				680531542,
				680531392,
			},

			// 680533090 "net/http.(*Transport).dialConnFor"
			695908230: {
				680531392,
				680533090,
			},
			696273159: { // BUG
				680531181,
				680531183,
				680531184,
				680533090,
			},
		}

		for _, ev := range data.Events {
			why := out.EventRegionStacks[ev]
			if need, ok := needBeenThere[ev.Ts]; ok {
				delete(needBeenThere, ev.Ts)
				var gs []uint64
				for try := why; try != nil; try = try.Parent {
					if l := len(gs); l == 0 || gs[l-1] != try.Start.G {
						gs = append(gs, try.Start.G)
					}
				}
				for i, j := 0, len(gs)-1; i < j; i, j = i+1, j-1 {
					gs[i], gs[j] = gs[j], gs[i]
				}
				if have, want := gs, need; !reflect.DeepEqual(have, want) {
					t.Errorf("Incorrect goroutine been-there list for %s\n%v\n!=\n%v", ev, have, want)
				}
			}
		}

		for ts, gs := range needBeenThere {
			t.Errorf("Did not encounter timestamp %d (expect to find goroutines %v there)", ts, gs)
		}
	})

	testcase := func(regionsFn func(*internal.Data) []*internal.Region, addCalls func(func(uint64, string, ...uint64))) func(t *testing.T) {
		return func(t *testing.T) {
			data := testhelp.Load(t, "../testdata/7de55ef9_go1.15.10/dial_own_tls_with_shared_dns")

			regions := regionsFn(data)
			out := rc.Process(&internal.ProcessRegionConnectionsInput{
				Data:    data,
				Regions: regions,
			})

			// Check that the following (goroutine, region kind) pairs exist, and that
			// they affect the subsequent set of goroutines.

			regionTouched := make(map[*internal.Region]map[uint64]struct{})
			for _, ev := range data.Events {
				for why := out.EventRegionStacks[ev]; why != nil; why = why.Parent {
					if why.Local != nil {
						m, ok := regionTouched[why.Local]
						if !ok {
							m = make(map[uint64]struct{})
							regionTouched[why.Local] = m
						}
						m[ev.G] = struct{}{}
					}
				}
			}

			wantRegions := make(map[string]struct{})
			regionString := func(g uint64, kind string, gs ...uint64) string {
				var b strings.Builder
				fmt.Fprintf(&b, "%d %q", g, kind)
				for _, g := range gs {
					fmt.Fprintf(&b, " %d", g)
				}
				return b.String()
			}
			add := func(g uint64, kind string, gs ...uint64) {
				gs = append([]uint64(nil), gs...)
				sort.Slice(gs, func(i, j int) bool { return gs[i] < gs[j] })
				wantRegions[regionString(g, kind, gs...)] = struct{}{}
			}
			addCalls(add)

			for region, m := range regionTouched {
				var gs []uint64
				for g := range m {
					gs = append(gs, g)
				}
				sort.Slice(gs, func(i, j int) bool { return gs[i] < gs[j] })

				str := regionString(region.Events[0].G, region.Kind, gs...)
				_, ok := wantRegions[str]
				delete(wantRegions, str)
				if !ok {
					t.Errorf("found unexpected region %s", str)
				}
			}
			for str := range wantRegions {
				t.Errorf("did not find region     %s", str)
			}

		}
	}

	t.Run("basic tracking", testcase(func(data *internal.Data) []*internal.Region {
		var regions []*internal.Region
		for _, fn := range []func([]*trace.Event) []*internal.Region{
			pattern.TrackHTTP1Writer,
			pattern.TrackHTTP1Reader,
			pattern.TrackHTTPClient,
		} {
			regions = append(regions, testhelp.FindAll(data, fn)...)
		}
		return regions
	}, func(add func(g uint64, kind string, gs ...uint64)) {
		// Connection A
		add(680531181, "client/http_roundtrip",
			680531181, // http.Transport.RoundTrip
			680531183, // dialConnFor
			680531184, // DNS doCall
			680533121, // DNS lookup
			680533122, // DNS lookup
			680532998, // TCP connect
			680533140, // addTLS
			680530334, // readLoop
			680530335, // writeLoop

			680531392, // BUG http.Transport.RoundTrip
			680533090, // BUG dialConnFor
			680533123, // BUG TCP connect
			680533141, // BUG addTLS
			680531542, // BUG readLoop
			680531543, // BUG writeLoop
		)
		add(680530334, "client/http_read", 680530334)
		add(680530335, "client/http_write", 680530335)

		// Connection B
		add(680531392, "client/http_roundtrip",
			680531392, // http.Transport.RoundTrip
			680533090, // dialConnFor
			// BUG 680533123, // TCP connect
			// BUG 680533141, // addTLS
			// BUG 680531542, // readLoop
			// BUG 680531543, // writeLoop
		)
		add(680531542, "client/http_read", 680531542)
		add(680531543, "client/http_write", 680531543)
	}))

	t.Run("add dialer", testcase(func(data *internal.Data) []*internal.Region {
		var regions []*internal.Region
		for _, fn := range []func([]*trace.Event) []*internal.Region{
			pattern.TrackHTTP1Writer,
			pattern.TrackHTTP1Reader,
			pattern.TrackHTTPClient,
			pattern.TrackHTTP1Dialer,
		} {
			regions = append(regions, testhelp.FindAll(data, fn)...)
		}
		return regions
	}, func(add func(g uint64, kind string, gs ...uint64)) {
		// Connection A
		add(680531181, "client/http_roundtrip",
			680531181, // http.Transport.RoundTrip
			680531183, // dialConnFor
			680531184, // DNS doCall
			680533121, // DNS lookup
			680533122, // DNS lookup
			680532998, // TCP connect
			680533140, // addTLS
			680530334, // readLoop
			680530335, // writeLoop
		)
		add(680531183, "client/http_dial",
			680531183, // dialConnFor
			680531184, // DNS doCall
			680533121, // DNS lookup
			680533122, // DNS lookup
			680532998, // TCP connect
			680533140, // addTLS
			680530334, // readLoop
			680530335, // writeLoop
		)
		add(680530334, "client/http_read", 680530334)
		add(680530335, "client/http_write", 680530335)

		// Connection B
		add(680531392, "client/http_roundtrip",
			680531392, // http.Transport.RoundTrip
			680533090, // dialConnFor
			680533123, // TCP connect
			680533141, // addTLS
			680531542, // readLoop
			680531543, // writeLoop
		)
		add(680533090, "client/http_dial",
			680533090, // dialConnFor
			680533123, // TCP connect
			680533141, // addTLS
			680531542, // readLoop
			680531543, // writeLoop
		)
		add(680531542, "client/http_read", 680531542)
		add(680531543, "client/http_write", 680531543)
	}))

	t.Run("add dns", testcase(func(data *internal.Data) []*internal.Region {
		var regions []*internal.Region
		for _, fn := range []func([]*trace.Event) []*internal.Region{
			pattern.TrackHTTP1Writer,
			pattern.TrackHTTP1Reader,
			pattern.TrackHTTPClient,
			pattern.TrackHTTP1DNS,
		} {
			regions = append(regions, testhelp.FindAll(data, fn)...)
		}
		return regions
	}, func(add func(g uint64, kind string, gs ...uint64)) {
		// Connection A
		add(680531181, "client/http_roundtrip",
			680531181, // http.Transport.RoundTrip
			680531183, // dialConnFor
			680531184, // DNS doCall
			680533121, // DNS lookup
			680533122, // DNS lookup
			680532998, // TCP connect
			680533140, // addTLS
			680530334, // readLoop
			680530335, // writeLoop
		)
		add(680531184, "client/http_dns",
			680531184, // DNS doCall
			680533121, // DNS lookup
			680533122, // DNS lookup
		)
		add(680530334, "client/http_read", 680530334)
		add(680530335, "client/http_write", 680530335)

		// Connection B
		add(680531392, "client/http_roundtrip",
			680531392, // http.Transport.RoundTrip
			680533090, // dialConnFor
			680533123, // TCP connect
			680533141, // addTLS
			680531542, // readLoop
			680531543, // writeLoop
		)
		add(680531542, "client/http_read", 680531542)
		add(680531543, "client/http_write", 680531543)
	}))

	t.Run("add dialer and dns", testcase(func(data *internal.Data) []*internal.Region {
		var regions []*internal.Region
		for _, fn := range []func([]*trace.Event) []*internal.Region{
			pattern.TrackHTTP1Writer,
			pattern.TrackHTTP1Reader,
			pattern.TrackHTTPClient,
			pattern.TrackHTTP1Dialer,
			pattern.TrackHTTP1DNS,
		} {
			regions = append(regions, testhelp.FindAll(data, fn)...)
		}
		return regions
	}, func(add func(g uint64, kind string, gs ...uint64)) {
		// Connection A
		add(680531181, "client/http_roundtrip",
			680531181, // http.Transport.RoundTrip
			680531183, // dialConnFor
			680531184, // DNS doCall
			680533121, // DNS lookup
			680533122, // DNS lookup
			680532998, // TCP connect
			680533140, // addTLS
			680530334, // readLoop
			680530335, // writeLoop
		)
		add(680531184, "client/http_dns",
			680531184, // DNS doCall
			680533121, // DNS lookup
			680533122, // DNS lookup
		)
		add(680531183, "client/http_dial",
			680531183, // dialConnFor
			680531184, // DNS doCall
			680533121, // DNS lookup
			680533122, // DNS lookup
			680532998, // TCP connect
			680533140, // addTLS
			680530334, // readLoop
			680530335, // writeLoop
		)
		add(680530334, "client/http_read", 680530334)
		add(680530335, "client/http_write", 680530335)

		// Connection B
		add(680531392, "client/http_roundtrip",
			680531392, // http.Transport.RoundTrip
			680533090, // dialConnFor
			680533123, // TCP connect
			680533141, // addTLS
			680531542, // readLoop
			680531543, // writeLoop
		)
		add(680533090, "client/http_dial",
			680533090, // dialConnFor
			680533123, // TCP connect
			680533141, // addTLS
			680531542, // readLoop
			680531543, // writeLoop
		)
		add(680531542, "client/http_read", 680531542)
		add(680531543, "client/http_write", 680531543)
	}))
}

func TestSimultaneousStart(t *testing.T) {
	// Here's an opportunity to add logging to or alter the connection rules.
	rc := &internal.RegionConnector{
		StartRegion: func(ev *trace.Event, existing, starting *internal.RegionStack) *internal.RegionStack {
			return (*internal.RegionConnector)(nil).DoStartRegion(ev, existing, starting)
		},
		ApplyOnWake: func(ev *trace.Event, existing, inbound *internal.RegionStack) *internal.RegionStack {
			return (*internal.RegionConnector)(nil).DoApplyOnWake(ev, existing, inbound)
		},
	}

	existing := &internal.RegionStack{
		Local: &internal.Region{Kind: "server/sdk"},
		Parent: &internal.RegionStack{
			Local: &internal.Region{Kind: "server/http"},
		},
	}
	starting := &internal.RegionStack{
		Local: &internal.Region{Kind: "client/sdk"},
		Parent: &internal.RegionStack{
			Local: &internal.Region{Kind: "client/http_roundtrip"},
		},
	}

	resulting := rc.DoStartRegion(new(trace.Event), existing, starting)

	var kinds []string
	for node := resulting; node != nil; node = node.Parent {
		if node.Local != nil {
			kinds = append(kinds, node.Local.Kind)
		}
	}

	have := kinds
	want := []string{"client/sdk", "client/http_roundtrip", "server/sdk", "server/http"}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("DoStartRegion;\n%q\n!=\n%q", have, want)
	}
}
