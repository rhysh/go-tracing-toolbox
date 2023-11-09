package testhelp

import (
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
	"github.com/rhysh/go-tracing-toolbox/internal/exectext"
)

func Load(t *testing.T, dir string) *internal.Data {
	var b strings.Builder

	fsys := os.DirFS(dir)
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "." {
				return nil
			}
			return fs.SkipDir
		}
		if !strings.HasSuffix(d.Name(), ".txt") {
			return nil
		}

		data, err := fs.ReadFile(fsys, d.Name())
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s", data)

		if len(data) > 0 && data[len(data)-1] != '\n' {
			return fmt.Errorf("no newline at end of file %q", path)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("fs.WalkDir(%q); err = %v", dir, err)
	}

	evs, err := exectext.ParseEvents(b.String())
	if err != nil {
		t.Fatalf("exectext.ParseEvents; err = %v", err)
	}
	sort.Slice(evs, func(i, j int) bool {
		ti, tj := evs[i].Ts, evs[j].Ts
		if ti != tj {
			return ti < tj
		}
		gi, gj := evs[i].G, evs[j].G
		return gi < gj
	})

	data := internal.PrepareData(evs)
	return data
}

func FindAll(data *internal.Data, fn func(evs []*trace.Event) []*internal.Region) []*internal.Region {
	var regions []*internal.Region
	for _, g := range data.GoroutineList {
		evs := data.GoroutineEvents[g]
		regions = append(regions, fn(evs)...)
	}
	return regions
}
