package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/pprof/profile"
)

func main() {
	root := flag.String("C", ".", "change to directory before running")

	requireTrace := flag.Bool("with-trace", false, "Filter to bundles that include an execution trace")
	doPrintHosts := flag.Bool("hosts", false, "print host list")
	doProfileSort := flag.String("profile-sort", "", `name of profile to use for sorting ("goroutine", "profile", etc)`)
	doWriteLinks := flag.Bool("write-links", false, "Create prev/next symlinks between bundles from the same process")
	vsPrev := flag.Bool("vs-prev", false, "Use the previous profile from the same process as a diff base")
	sampleType := flag.String("sample-type", "", `Name of sample type to use for sorting ("inuse_space", "alloc_objects", "contentions", etc)`)

	var focus []*regexp.Regexp
	flag.Func("focus", "Filter profile samples to matching stacks", func(s string) error {
		re, err := regexp.Compile(s)
		if err != nil {
			return err
		}
		focus = append(focus, re)
		return nil
	})

	var ignore *regexp.Regexp
	flag.Func("ignore", "Filter profile samples to remove matching stacks", func(s string) error {
		re, err := regexp.Compile(s)
		if err != nil {
			return err
		}
		if ignore != nil {
			return fmt.Errorf("this flag may only be provided once")
		}
		ignore = re
		return nil
	})

	flag.Parse()

	err := os.Chdir(*root)
	if err != nil {
		log.Fatalf("os.Chdir: %v", err)
	}

	fsys := os.DirFS(".")

	files, err := allFiles(fsys)
	if err != nil {
		log.Fatalf("allFiles; err = %v", err)
	}

	allMeta, err := readMetas(fsys, files)
	if err != nil {
		log.Fatalf("readMetas; err = %v", err)
	}

	allMeta = sortedMetas(allMeta)

	if *doWriteLinks {
		err := writeLinks(allMeta)
		if err != nil {
			log.Fatalf("create symlink tree: %v", err)
		}
		return
	}

	if *requireTrace {
		var ms []*meta
		for _, m := range allMeta {
			if stat, err := fs.Stat(fsys, path.Join(m.dir, "pprof/trace")); err == nil && stat.Mode().IsRegular() {
				ms = append(ms, m)
			}
		}
		allMeta = ms
	}

	procMeta := make(map[string][]*meta)
	for _, m := range allMeta {
		procMeta[m.ProcID] = append(procMeta[m.ProcID], m)
	}

	if *doPrintHosts {
		printHosts(allMeta)
		return
	}

	if *doProfileSort != "" {
		values := profileCount(fsys, allMeta, focus, ignore, path.Clean(*doProfileSort), *sampleType)
		sorted := profileSort(values)

		if *vsPrev {
			// This compares against the previous profile from the same instance
			// of the process. Usually that will be from the previous bundle,
			// though there are some cases (CPU profiling, for example) when a
			// process won't include a particular profile in each bundle, which
			// will cause this behavior to be different (better?) than a user
			// would get from running
			//   "go tool pprof -base {_link/prev/,}pprof/foo"
			prev := findPrev(sorted)
			delta := make(map[*meta]*big.Int)
			for m1, m0 := range prev {
				delta[m1] = new(big.Int).Sub(values[m1], values[m0])
			}
			values = delta
			sorted = profileSort(values)
		}

		printCounts(sorted, values)
		return
	}
}

func allFiles(fsys fs.FS) ([]string, error) {
	var files []string
	err := fs.WalkDir(fsys, ".",
		func(path string, d fs.DirEntry, err error) error {
			if d == nil {
				return err
			}
			if err != nil {
				return err
			}

			if d.Type().IsRegular() {
				files = append(files, path)
			}
			return nil
		},
	)
	return files, err
}

func readMetas(fsys fs.FS, files []string) ([]*meta, error) {
	var ms []*meta
	for _, file := range files {
		if path.Base(file) != "meta" {
			continue
		}

		buf, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("ReadFile(%q): %w", file, err)
		}
		var m meta
		err = json.Unmarshal(buf, &m)
		if err != nil {
			return nil, fmt.Errorf("Unmarshal(%q): %w", file, err)
		}
		m.dir = path.Dir(file)
		ms = append(ms, &m)
	}

	return ms, nil
}

func sortedMetas(ms []*meta) []*meta {
	if len(ms) == 0 {
		return nil
	}
	ms = append(([]*meta)(nil), ms...)
	sort.Slice(ms, func(i, j int) bool {
		if !ms[i].CaptureTime.Equal(ms[j].CaptureTime) {
			return ms[i].CaptureTime.Before(ms[j].CaptureTime)
		}
		return ms[i].ProcID < ms[j].ProcID
	})
	// deduplicate
	ms2 := []*meta{ms[0]}
	for i := 1; i < len(ms); i++ {
		if ms[i].ProcID == ms[i-1].ProcID && ms[i].CaptureTime.Equal(ms[i-1].CaptureTime) {
			continue
		}
		ms2 = append(ms2, ms[i])
	}
	return ms2
}

func printHosts(ms []*meta) {
	hostSet := make(map[string]struct{})
	for _, m := range ms {
		host := m.Hostname
		hostSet[host] = struct{}{}
	}

	var hosts []string
	for host := range hostSet {
		hosts = append(hosts, host)
	}
	sort.Slice(hosts, func(i, j int) bool {
		si := strings.Split(hosts[i], ".")
		for k := 0; k < len(si)/2; k++ {
			si[k], si[len(si)-1-k] = si[len(si)-1-k], si[k]
		}

		sj := strings.Split(hosts[j], ".")
		for k := 0; k < len(sj)/2; k++ {
			sj[k], sj[len(sj)-1-k] = sj[len(sj)-1-k], sj[k]
		}

		for k := 0; k < len(si) && k < len(sj); k++ {
			if si[k] != sj[k] {
				return si[k] < sj[k]
			}
		}
		if len(si) != len(sj) {
			return len(si) < len(sj)
		}
		return false
	})

	for _, host := range hosts {
		fmt.Printf("%s\n", host)
	}
}

func printCounts(order []*meta, values map[*meta]*big.Int) {
	for _, m := range order {
		v := values[m]
		fmt.Printf("%v %v\n", m.dir, v)
	}
}

func profileSort(values map[*meta]*big.Int) []*meta {
	var sorted []*meta
	for m := range values {
		sorted = append(sorted, m)
	}

	sort.Slice(sorted, func(i, j int) bool {
		mi, mj := sorted[i], sorted[j]
		vi, vj := values[mi], values[mj]
		if cmp := vi.Cmp(vj); cmp != 0 {
			return cmp < 0
		}

		// tiebreak
		if !sorted[i].CaptureTime.Equal(sorted[j].CaptureTime) {
			return sorted[i].CaptureTime.Before(sorted[j].CaptureTime)
		}
		return sorted[i].ProcID < sorted[j].ProcID
	})

	for k := 0; k < len(sorted)/2; k++ {
		sorted[k], sorted[len(sorted)-1-k] = sorted[len(sorted)-1-k], sorted[k]
	}

	return sorted
}

func profileCount(fsys fs.FS, ms []*meta, focus []*regexp.Regexp, ignore *regexp.Regexp, profileName string, sampleType string) map[*meta]*big.Int {
	values := make(map[*meta]*big.Int)
	for _, m := range ms {
		buf, err := fs.ReadFile(fsys, path.Join(m.dir, "pprof", profileName))
		if err != nil {
			// not all bundles include all profile types. ok to skip.
			continue
		}
		prof, err := profile.Parse(bytes.NewReader(buf))
		if err != nil {
			continue
		}

		sampleIndex := 0
		for i, t := range prof.SampleType {
			want := sampleType
			if want == "" {
				want = prof.DefaultSampleType
			}
			if t.Type == want {
				sampleIndex = i
				break
			}
		}

		prof.FilterSamplesByName(nil, ignore, nil, nil)
		for _, re := range focus {
			prof.FilterSamplesByName(re, nil, nil, nil)
		}

		sum := new(big.Int)
		values[m] = sum

		for _, s := range prof.Sample {
			val := s.Value[sampleIndex]
			sum.Add(sum, big.NewInt(val))
		}
	}

	return values
}

type meta struct {
	dir         string    `json:"-"`
	Main        string    `json:"main"`
	Revision    string    `json:"revision"`
	GoVersion   string    `json:"go_version"`
	Hostname    string    `json:"hostname"`
	ProcID      string    `json:"proc_id"`
	InitTime    time.Time `json:"init_time"`
	CaptureTime time.Time `json:"capture_time"`
}

func findPrev(allMeta []*meta) map[*meta]*meta {
	byProc := make(map[string][]*meta)
	for _, m := range allMeta {
		byProc[m.ProcID] = append(byProc[m.ProcID], m)
	}

	prev := make(map[*meta]*meta)
	for _, ms := range byProc {
		sort.Slice(ms, func(i, j int) bool { return ms[i].CaptureTime.Before(ms[j].CaptureTime) })
		for i := 1; i < len(ms); i++ {
			prev[ms[i]] = ms[i-1]
		}
	}

	return prev
}

func writeLinks(allMeta []*meta) error {
	byProc := make(map[string][]*meta)
	for _, m := range allMeta {
		byProc[m.ProcID] = append(byProc[m.ProcID], m)
	}
	for _, ms := range byProc {
		sort.Slice(ms, func(i, j int) bool { return ms[i].CaptureTime.Before(ms[j].CaptureTime) })
	}

	for _, ms := range byProc {
		for i := 1; i < len(ms); i++ {
			prev, curr := ms[i-1], ms[i]
			err := writeOneLink(prev, curr, "prev")
			if err != nil {
				return err
			}
			err = writeOneLink(curr, prev, "next")
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func writeOneLink(to, from *meta, name string) error {
	rel, err := filepath.Rel(from.dir, to.dir)
	if err != nil {
		return err
	}
	err = os.Mkdir(filepath.Join(from.dir, "_link"), 0777)
	if err != nil {
		if e := new(os.PathError); errors.As(err, &e) && errors.Is(e.Err, fs.ErrExist) {
		} else {
			return err
		}
	}

	linkname := filepath.Join(from.dir, "_link", name)
	info, err := os.Lstat(linkname)
	if err != nil {
		if e := new(os.PathError); errors.As(err, &e) && errors.Is(e.Err, fs.ErrNotExist) {
			// ignore
		} else {
			return err
		}
	} else {
		if info.Mode()&fs.ModeSymlink != 0 {
			err := os.Remove(linkname)
			if err != nil {
				return err
			}
		}
	}
	err = os.Symlink(filepath.Join("..", rel), linkname)
	if err != nil {
		if e := new(os.LinkError); errors.As(err, &e) && errors.Is(e.Err, fs.ErrExist) {
		} else {
			return err
		}
	}
	return nil
}
