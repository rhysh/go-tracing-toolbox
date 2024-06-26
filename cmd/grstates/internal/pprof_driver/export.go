package driver

//go:generate cp $GOROOT/src/cmd/vendor/github.com/google/pprof/internal/driver/svg.go ./pprof_svg.go

func MassageSVG(svg string) string { return massageSVG(svg) }
