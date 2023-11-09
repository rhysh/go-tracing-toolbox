package pattern_test

import (
	"testing"

	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/pattern"
	"github.com/rhysh/go-tracing-toolbox/internal/testhelp"
)

func logRegions(t *testing.T, regions []*internal.Region) {
	for _, reg := range regions {
		ext := internal.EventList(reg.Events).Extent()
		t.Logf("g %d kind %q start %d end %d dur %s", reg.Events[0].G, reg.Kind,
			ext.Start().Nanoseconds(), ext.End().Nanoseconds(), ext.Length())
	}
}

func checkRegions(t *testing.T, regions []*internal.Region, kind string, goroutineStartEndTimes ...int64) {
	for _, reg := range regions {
		ext := internal.EventList(reg.Events).Extent()
		if kind != reg.Kind {
			continue
		}
		if len(goroutineStartEndTimes) < 3 {
			t.Errorf("found %q region, but no expected goroutine/start/end time trio", kind)
			continue
		}
		goroutine := uint64(goroutineStartEndTimes[0])
		start, end := goroutineStartEndTimes[1], goroutineStartEndTimes[2]
		goroutineStartEndTimes = goroutineStartEndTimes[3:]

		if have, want := reg.Events[0].G, goroutine; have != want {
			t.Errorf("incorrect goroutine for %q region; %d != %d", kind, have, want)
		}

		if have, want := ext.Start().Nanoseconds(), start; have != want {
			t.Errorf("incorrect start time for %q region; %d != %d", kind, have, want)
		}
		if have, want := ext.End().Nanoseconds(), end; have != want {
			t.Errorf("incorrect end time for %q region; %d != %d", kind, have, want)
		}
	}
	if remains := len(goroutineStartEndTimes); remains != 0 {
		t.Errorf("have %d leftover goroutines/starts/ends for %q regions", remains, kind)
	}
}

func TestRegions(t *testing.T) {
	data := testhelp.Load(t, "../../testdata/7de55ef9_go1.15.10/dial_own_tls_with_shared_dns")
	t.Run("client/http_write", func(t *testing.T) {
		checkRegions(t, testhelp.FindAll(data, pattern.TrackHTTP1Writer), "client/http_write",
			680530335, 702489253, 702524304,
			680531543, 703591527, 703627794,
		)
	})
	t.Run("client/http_read", func(t *testing.T) {
		checkRegions(t, testhelp.FindAll(data, pattern.TrackHTTP1Reader), "client/http_read",
			680530334, 702438203, 727206110,
			680531542, 703558545, 745909754,
		)
	})
	t.Run("client/http_roundtrip", func(t *testing.T) {
		checkRegions(t, testhelp.FindAll(data, pattern.TrackHTTPClient), "client/http_roundtrip",
			680531181, 695730843, 727242718,
			680531392, 695888518, 745939343,
		)
	})
	t.Run("client/http_dial", func(t *testing.T) {
		checkRegions(t, testhelp.FindAll(data, pattern.TrackHTTP1Dialer), "client/http_dial",
			680531183, 695758811, 702432357,
			680533090, 695918961, 703532327,
		)
	})
	t.Run("client/http_dns", func(t *testing.T) {
		checkRegions(t, testhelp.FindAll(data, pattern.TrackHTTP1DNS), "client/http_dns",
			680531184, 695785500, 696203164,
		)
	})
}

func TestHTTP1ServerRegions(t *testing.T) {
	data := testhelp.Load(t, "../../testdata/7de55ef9_go1.15.10/http_conn_serve")
	t.Run("server/http", func(t *testing.T) {
		regions := testhelp.FindAll(data, pattern.TrackHTTP1Server)
		checkRegions(t, regions, "server/http_read",
			680534068, 892977658, 892977658,
			680534068, 904581002, 908233530,
			680534068, 948263071, 950621923,
			680534068, 960975730, 964130081,
			680534068, 965562894, 970713280,
			// And the final (partial) region starts at 996295375
		)
		checkRegions(t, regions, "server/http_write",
			680534068, 904538378, 904578848,
			680534068, 948229770, 948261343,
			680534068, 960942450, 960974066,
			680534068, 965526136, 965560419,
			680534068, 996260282, 996293199,
		)
		checkRegions(t, regions, "server/http",
			680534068, 892977658, 904538378,
			680534068, 908233530, 948229770,
			680534068, 950621923, 960942450,
			680534068, 964130081, 965526136,
			680534068, 970713280, 996260282,
		)
	})
}

func TestGCAssist(t *testing.T) {
	data := testhelp.Load(t, "../../testdata/f2f5b4bd_go1.18.7/gc_assist")
	t.Run("", func(t *testing.T) {
		regions := testhelp.FindAll(data, pattern.TrackAll)
		var keep []*internal.Region
		for _, r := range regions {
			if 1185138955 <= r.Events[0].Ts && r.Events[len(r.Events)-1].Ts <= 1877568374 {
				keep = append(keep, r)
			}
		}
		regions = keep
		logRegions(t, regions)
		checkRegions(t, regions, "server/http",
			130809, 1_185_138_955, 1_877_568_374,
		)
		checkRegions(t, regions, "client/http_roundtrip",
			130809, 1_827_393_328, 1_875_371_807,
			67059294, 1_331_823_807, 1_359_513_355,
			67059297, 1_319_046_532, 1_348_449_260,
		)
	})
}
