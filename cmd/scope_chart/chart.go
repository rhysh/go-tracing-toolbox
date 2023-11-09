package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	svg "github.com/ajstarks/svgo"
)

type sample struct {
	frac  float64
	stack []string
}

func main() {
	inFile := flag.String("input", "", "")
	flag.Parse()

	var rd io.Reader = strings.NewReader(input)
	if *inFile != "" {
		buf, err := os.ReadFile(*inFile)
		if err != nil {
			log.Fatalf("os.ReadFile(%q): %v", *inFile, err)
		}
		rd = bytes.NewReader(buf)
	}

	var samples []sample
	sc := bufio.NewScanner(rd)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) != 8 {
			continue
		}
		frac, err := strconv.ParseFloat(fs[5], 64)
		if err != nil {
			continue
		}
		stack := strings.Split(fs[7], ";")
		if len(stack) >= 1 {
			stack = stack[1:]
		}

		samples = append(samples, sample{frac: frac, stack: stack})
	}

	stackMap := make(map[string][]string)
	for _, sample := range samples {
		stack := sample.stack
		stackMap[strings.Join(stack, ";")] = stack
	}

	// TODO: find a better (less garbage-intensive) map key for stacks
	baseWeight := make(map[string]int)
	for _, sample := range samples {
		stack := sample.stack
		for i := range stack {
			baseWeight[strings.Join(stack[:i+1], ";")]++
		}
	}
	baseWeight = make(map[string]int)
	for _, stack := range stackMap {
		for i := range stack {
			baseWeight[strings.Join(stack[:i+1], ";")]++
		}
	}

	var stacks [][]string
	for _, stack := range stackMap {
		stacks = append(stacks, stack)
	}
	sort.Slice(stacks, func(i, j int) bool {
		si, sj := stacks[i], stacks[j]
		for k := 0; ; k++ {
			if k == len(si) {
				// stack i is shorter than stack j, so sorts earlier
				return true
			}
			if k == len(sj) {
				return false
			}
			if si[k] == sj[k] {
				continue
			}
			return si[k] < sj[k]
		}
	})

	// TODO: find a better (less garbage-intensive) map key for stacks
	stackOrder := make(map[string]int)
	for i, stack := range stacks {
		stackOrder[strings.Join(stack, ";")] = i + 1
	}

	sort.Slice(samples, func(i, j int) bool {
		si, sj := samples[i], samples[j]
		if si.frac != sj.frac {
			return si.frac < sj.frac
		}
		orderI := stackOrder[strings.Join(si.stack, ";")]
		orderJ := stackOrder[strings.Join(sj.stack, ";")]
		return orderI < orderJ
	})

	const (
		laneWidth  = 30
		laneHeight = 800
		lineWidth  = 1
		markSize   = 8
	)

	var (
		backgroundColor = color.RGBA{255, 255, 255, 255}
		markColor       = color.RGBA{0, 0, 0, 255}
		lineColor       = color.RGBA{255, 0, 0, 255}
	)

	img := image.NewNRGBA(image.Rect(0, 0, len(stackOrder)*laneWidth, laneHeight))

	draw.Draw(img, img.Bounds(), &image.Uniform{backgroundColor}, image.Point{}, draw.Src)

	for i := range stacks {
		if i == 0 {
			continue
		}
		lhs, rhs := stacks[i-1], stacks[i]
		var shared int
		for j := 0; ; j++ {
			if j == len(lhs) || j == len(rhs) || lhs[j] != rhs[j] {
				break
			}
			shared++
		}
		uniqL := len(lhs) - shared
		uniqR := len(rhs) - shared

		keep := 0
		for j := 0; j <= shared; j++ {
			v := baseWeight[strings.Join(lhs[:j], ";")]
			keep += v
		}

		remove := 0
		for j := shared + 1; j <= len(lhs); j++ {
			v := baseWeight[strings.Join(lhs[:j], ";")]
			remove += v
		}

		add := 0
		for j := shared + 1; j <= len(rhs); j++ {
			v := baseWeight[strings.Join(rhs[:j], ";")]
			add += v
		}

		diff := 0.0
		if uniqL > 0 {
			diff += float64(remove) / float64(uniqL)
		}
		if uniqR > 0 {
			diff += float64(add) / float64(uniqR)
		}
		diff /= (float64(keep) / float64(shared))

		drawLine := func(nudge int, weight float64) {
			line := image.Rect(0, 0, lineWidth, laneHeight)
			line = line.Add(image.Pt(i*laneWidth, 0))

			strength := float64(weight) * 255
			if strength > 255 {
				strength = 255
			}
			alpha := uint8(strength)

			draw.DrawMask(img, line.Add(image.Pt(nudge, 0)),
				&image.Uniform{lineColor}, image.Point{},
				&image.Uniform{color.RGBA{0, 0, 0, alpha}}, image.Point{},
				draw.Over)
		}

		drawLine(0, diff)
	}

	for _, sample := range samples {
		lane := stackOrder[strings.Join(sample.stack, ";")]
		where := image.Pt(lane*laneWidth-laneWidth/2, int(sample.frac*laneHeight))
		mark := image.Rect(-markSize, -markSize, markSize, markSize)
		mark = mark.Add(where)
		draw.DrawMask(img, mark,
			&image.Uniform{markColor}, image.Point{},
			&image.Uniform{color.RGBA{0, 0, 0, 127}}, image.Point{},
			draw.Over)
	}

	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	if err != nil {
		log.Fatalf("png.Encode: %v", err)
	}
	err = os.WriteFile("/tmp/img.png", buf.Bytes(), 0600)
	if err != nil {
		log.Fatalf("os.WriteFile: %v", err)
	}

	// SVG
	const (
		frameLimit    = 5
		stackTextFill = "#AAAAAA"
		stackTextSize = "10px"
		border        = 5
	)

	stackSamples := make([][]sample, len(stacks))
	for _, sample := range samples {
		order := stackOrder[strings.Join(sample.stack, ";")]
		stackSamples[order-1] = append(stackSamples[order-1], sample)
	}

	var buf2 bytes.Buffer
	img2 := svg.New(&buf2)
	img2.Start(laneHeight+2*border, laneWidth*len(stackOrder)+2*border)
	img2.Gtransform(fmt.Sprintf("translate(%d,%d)", border, border))
	for i, stack := range stacks {
		img2.Gtransform(fmt.Sprintf("translate(%d,%d)", 0, laneWidth*i))

		img2.Title(strings.Join(stack, "\n"))
		img2.Rect(0, 0, laneHeight, laneWidth, `opacity="0"`)
		for _, sample := range stackSamples[i] {
			x := int(float64(laneHeight) * sample.frac)
			img2.Rect(x-markSize, 0+markSize, 2*markSize, 2*markSize, fmt.Sprintf(`opacity="%f"`, 0.5))
		}

		var start, end string
		if len(stack) <= frameLimit {
			start = strings.Join(stack, ";")
		} else {
			start = strings.Join(stack[:frameLimit], ";") + " ..."
			rest := stack[frameLimit:]
			end = "... "
			if len(rest) > frameLimit {
				end = fmt.Sprintf("... [skip %d] ... ", len(rest)-frameLimit)
				rest = rest[len(rest)-frameLimit:]
			}
			end += strings.Join(rest, ";")
		}
		img2.Text(0, laneWidth/3, start,
			`alignment-baseline="middle"`,
			fmt.Sprintf("font-size=%q", stackTextSize),
			fmt.Sprintf("fill=%q", stackTextFill))
		if end != "" {
			img2.Text(laneHeight, laneWidth-laneWidth/3, end,
				`alignment-baseline="middle"`,
				fmt.Sprintf("font-size=%q", stackTextSize),
				fmt.Sprintf("fill=%q", stackTextFill), `text-anchor="end"`)
		}
		img2.Gend()
	}
	img2.Gend()
	img2.End()

	err = os.WriteFile("/tmp/img.svg", buf2.Bytes(), 0600)
	if err != nil {
		log.Fatalf("os.WriteFile: %v", err)
	}
}

var input = `
start 108325136 length 7790533 frac 0.059467 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 42287493 length 8706247 frac 0.060095 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 252255907 length 6842289 frac 0.095242 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 994431892 length 8034186 frac 0.100135 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 663937167 length 8601275 frac 0.123194 stack runtime.gcAssistAlloc;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 641685895 length 8919247 frac 0.139016 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*memRecordCycle).add
start 873832154 length 8952404 frac 0.142641 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 255264405 length 6658948 frac 0.178772 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*memRecordCycle).add
start 260461596 length 9681558 frac 0.196626 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 343090766 length 7422145 frac 0.220539 stack runtime.gcAssistAlloc;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 63645799 length 7286963 frac 0.223572 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 430943203 length 8528872 frac 0.245161 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 603312610 length 8339104 frac 0.252472 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*memRecordCycle).add
start 731259091 length 8045415 frac 0.256536 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 848923938 length 8093649 frac 0.270783 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 692548470 length 7763778 frac 0.315171 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 774544914 length 7869901 frac 0.316513 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 308355762 length 8483756 frac 0.326287 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*memRecordCycle).add
start 383562626 length 9367606 frac 0.327118 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 492881226 length 8976683 frac 0.328987 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 198219960 length 7667317 frac 0.332359 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 684994968 length 9010903 frac 0.340385 stack runtime.gcAssistAlloc;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 410582973 length 8715321 frac 0.340603 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*bucket).mp
start 314147583 length 9836437 frac 0.345748 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 774507584 length 8824297 frac 0.365256 stack runtime.gcAssistAlloc;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 399811812 length 8538759 frac 0.434963 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 730646960 length 7717550 frac 0.444882 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 45219739 length 6946725 frac 0.453880 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 610804779 length 6970223 frac 0.475463 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 231631709 length 8300507 frac 0.478908 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 259427692 length 8982449 frac 0.486322 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 134900713 length 7291412 frac 0.486652 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 348182733 length 8316848 frac 0.495966 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*bucket).mp
start 738533937 length 9310531 frac 0.497700 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*bucket).mp
start 838494028 length 7985422 frac 0.497880 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 98533494 length 8215435 frac 0.499527 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*bucket).mp
start 826831199 length 9200085 frac 0.525805 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*memRecordCycle).add
start 655255132 length 9112251 frac 0.530960 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*memRecordCycle).add
start 346894737 length 9344570 frac 0.531716 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*bucket).mp
start 663159108 length 7123654 frac 0.589332 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 607611566 length 8160142 frac 0.604227 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 298812136 length 8939429 frac 0.631458 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*bucket).mp
start 529028745 length 9233966 frac 0.642558 stack runtime.gcAssistAlloc;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 533948867 length 7485566 frac 0.658976 stack runtime.gcAssistAlloc;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 664065507 length 7892033 frac 0.710984 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 561731668 length 8656065 frac 0.716031 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*memRecordCycle).add
start 654268714 length 8583362 frac 0.736201 stack runtime.gcAssistAlloc;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 472149773 length 7948339 frac 0.746559 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked
start 218658833 length 8276282 frac 0.763825 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.freeStackSpans;runtime.(*mheap).freeManual;runtime.(*mheap).freeSpanLocked
start 904508361 length 8238922 frac 0.765775 stack runtime.gcAssistAlloc;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.mProf_Flush;runtime.mProf_FlushLocked;runtime.(*bucket).mp
start 79501023 length 9814294 frac 0.774881 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.freeStackSpans;runtime.(*mheap).freeManual;runtime.lock;runtime.lockWithRank;runtime.lock2;runtime.procyield
start 698625522 length 9284001 frac 0.782838 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.freeStackSpans
start 538117230 length 8869530 frac 0.784098 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.freeStackSpans;runtime.(*mheap).freeManual;runtime.unlock;runtime.unlockWithRank;runtime.unlock2;runtime.futexwakeup;runtime.futex
start 415884783 length 7385778 frac 0.826411 stack runtime.gcAssistAlloc;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.freeStackSpans;runtime.(*mheap).freeManual;runtime.(*mheap).freeSpanLocked;runtime.(*pageAlloc).free;runtime.(*pageAlloc).update;runtime.mergeSummaries
start 586439094 length 7654408 frac 0.834060 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.gcMarkTermination.func4;runtime.forEachP;runtime.gcMarkTermination.func4.1;runtime.(*mcache).prepareForSweep;runtime.stackcache_clear;runtime.stackpoolfree
start 95855821 length 8487133 frac 0.884773 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.freeStackSpans;runtime.(*mheap).freeManual;runtime.lock;runtime.lockWithRank;runtime.lock2;runtime.procyield
start 238747394 length 7568105 frac 0.895725 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.gcMarkTermination.func4;runtime.forEachP;runtime.gcMarkTermination.func4.1;runtime.(*mcache).prepareForSweep;runtime.(*mcache).releaseAll
start 309593501 length 6291015 frac 0.898369 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.gcMarkTermination.func4;runtime.forEachP;runtime.gcMarkTermination.func4.1;runtime.(*mcache).prepareForSweep;runtime.(*mcache).releaseAll;runtime.(*mcentral).uncacheSpan;runtime.(*sweepLocked).sweep;runtime.(*consistentHeapStats).release
start 815608241 length 8394430 frac 0.904428 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.freeStackSpans;runtime.(*mheap).freeManual;runtime.unlock;runtime.unlockWithRank;runtime.unlock2;runtime.futexwakeup;runtime.futex
start 340988353 length 8690493 frac 0.928219 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.gcMarkTermination.func4;runtime.forEachP;runtime.gcMarkTermination.func4.1;runtime.(*mcache).prepareForSweep;runtime.(*mcache).releaseAll;runtime.(*mcentral).uncacheSpan;runtime.(*sweepLocked).sweep;runtime.divRoundUp
start 22475706 length 9076021 frac 0.951304 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.gcMarkTermination.func4;runtime.forEachP;runtime.gcMarkTermination.func4.1;runtime.(*mcache).prepareForSweep;runtime.(*mcache).releaseAll
start 710162928 length 10439406 frac 0.963073 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.gcMarkTermination.func4;runtime.forEachP;runtime.gcMarkTermination.func4.1;runtime.(*mcache).prepareForSweep;runtime.(*mcache).releaseAll;runtime.(*mcentral).uncacheSpan;runtime.(*sweepLocked).sweep;runtime.(*mheap).freeSpan;runtime.(*mheap).freeSpan.func1;runtime.(*mheap).freeSpanLocked;runtime.(*pageAlloc).free;runtime.(*pageAlloc).update;runtime.(*pallocBits).summarize
start 102352896 length 7531548 frac 0.975728 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.freeStackSpans;runtime.(*mSpanList).remove
start 777471783 length 8762552 frac 0.982356 stack runtime.gcBgMarkWorker;runtime.gcMarkDone;runtime.gcMarkTermination;runtime.systemstack;runtime.gcMarkTermination.func4;runtime.forEachP;runtime.gcMarkTermination.func4.1;runtime.(*mcache).prepareForSweep;runtime.(*mcache).releaseAll;runtime.(*mcentral).uncacheSpan;runtime.(*sweepLocked).sweep;runtime.(*mheap).freeSpan;runtime.(*mheap).freeSpan.func1;runtime.(*mheap).freeSpanLocked;runtime.(*pageAlloc).free;runtime.(*pageAlloc).update;runtime.mergeSummaries
`[1:]
