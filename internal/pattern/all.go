package pattern

import (
	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func TrackAll(evs []*trace.Event) []*internal.Region {
	var regions []*internal.Region
	for _, fn := range []func([]*trace.Event) []*internal.Region{
		TrackHTTP1Writer,
		TrackHTTP1Reader,
		TrackHTTPClient,
		TrackHTTP1Dialer,
		TrackHTTP1DNS,
		TrackHTTP1Server,
	} {
		regions = append(regions, fn(evs)...)
	}
	return regions
}
