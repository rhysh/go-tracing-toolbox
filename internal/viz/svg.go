package viz

import (
	"bytes"
	"fmt"
	"sort"

	svg "github.com/ajstarks/svgo"
	"github.com/rhysh/go-tracing-toolbox/internal/cluster"
)

const (
	imgWidth          = 1000
	imgBarHeight      = 5
	imgGap            = 2
	imgSectionGap     = 10
	imgSectionFooter  = 50
	imgTextDx         = 2
	imgTextDy         = imgBarHeight - 1
	imgTextSize       = "4pt"
	imgTextColor      = "grey"
	imgSeparatorFill  = "lightgrey"
	imgBarFillBug     = "magenta"
	imgBarFillRunning = "black"
	imgBarFillAssist  = "lawngreen"
	imgBarFillWaitCPU = "silver"
	imgBarFillNetwork = "yellow"
	imgBarFillSyscall = "darkgreen"
	imgBarFillBlocked = "violet"
)

type Job struct {
	spans []*cluster.Span

	barCount      int
	sectionHeight int

	minTs, maxTs int64
	maxLength    int64
}

func (j *Job) calculateSize() {
	for i, sp := range j.spans {
		if start := sp.StartNs; start < j.minTs || i == 0 {
			j.minTs = start
		}
		if end := sp.StartNs + sp.LengthNs; end > j.maxTs {
			j.maxTs = end
		}
		if l := sp.LengthNs; l > j.maxLength {
			j.maxLength = l
		}

		cluster.Visit(sp, func(span *cluster.Span) {
			j.barCount++
		})
	}

	j.sectionHeight = imgBarHeight*j.barCount + imgGap*(len(j.spans)-1)
}

func Render(spans []*cluster.TreeSummary, expand bool) []byte {
	var buf bytes.Buffer
	j := &Job{}
	for _, v := range spans {
		if expand {
			j.spans = append(j.spans, v.Root)
		} else {
			j.spans = append(j.spans, v.Flat)
		}
	}
	j.calculateSize()

	img := svg.New(&buf)

	var (
		barCount     int
		minTs, maxTs int64
		maxLength    int64
	)

	for i, root := range j.spans {
		if start := root.StartNs; start < minTs || i == 0 {
			minTs = start
		}
		if end := root.StartNs + root.LengthNs; end > maxTs {
			maxTs = end
		}
		if l := root.LengthNs; l > maxLength {
			maxLength = l
		}

		if expand {
			cluster.Visit(root, func(span *cluster.Span) {
				barCount++
			})
		} else {
			barCount++ // summary
		}
	}
	sectionHeight := imgBarHeight*barCount + imgGap*(len(spans)-1)
	img.Start(imgWidth, (sectionHeight+imgSectionFooter)*2+imgSectionGap)

	spansByStart := append([]*cluster.TreeSummary(nil), spans...)
	sort.Slice(spansByStart, func(i, j int) bool { return spansByStart[i].Root.StartNs < spansByStart[j].Root.StartNs })

	r := &Ribbon{widthNs: maxTs - minTs, widthPx: imgWidth, y: 0}
	for _, root := range spansByStart {
		if !expand {
			label := fmt.Sprintf("g=%d @%0.6fms / %0.6fms / on CPU %0.6fms %s", root.Flat.G,
				float64(root.Flat.StartNs)/1e6, float64(root.LengthNs)/1e6, float64(root.TotalRunNs)/1e6, root.Flat.Kind)
			renderSpan(img, r, minTs, root.Flat, label)
			r.y += imgBarHeight
			continue
		}
		cluster.Visit(root.Root, func(span *cluster.Span) {
			label := fmt.Sprintf("g=%d +%0.6fms / %0.6fms %s", span.G,
				float64(span.StartNs-root.Root.StartNs)/1e6, float64(span.LengthNs)/1e6, span.Kind)
			if span == root.Root {
				label = fmt.Sprintf("g=%d @%0.6fms / %0.6fms %s", span.G,
					float64(span.StartNs)/1e6, float64(span.LengthNs)/1e6, span.Kind)
			}

			renderSpan(img, r, minTs, span, label)

			r.y += imgBarHeight
		})
		if imgSeparatorFill != "" {
			img.Rect(0, r.y, r.widthPx, imgGap, fmt.Sprintf("fill=%q", imgSeparatorFill))
		}
		r.y += imgGap
	}

	r.y += imgSectionFooter
	r.y += imgSectionGap

	r.widthNs = maxLength
	for _, root := range spansByStart {
		if !expand {
			label := fmt.Sprintf("g=%d @%0.6fms / %0.6fms / on CPU %0.6fms %s", root.Flat.G,
				float64(root.Flat.StartNs)/1e6, float64(root.LengthNs)/1e6, float64(root.TotalRunNs)/1e6, root.Flat.Kind)
			renderSpan(img, r, root.Flat.StartNs, root.Flat, label)
			r.y += imgBarHeight
			continue
		}
		cluster.Visit(root.Root, func(span *cluster.Span) {
			label := fmt.Sprintf("g=%d +%0.6fms / %0.6fms %s", span.G,
				float64(span.StartNs-root.Root.StartNs)/1e6, float64(span.LengthNs)/1e6, span.Kind)
			if span == root.Root {
				label = fmt.Sprintf("g=%d @%0.6fms / %0.6fms %s", span.G,
					float64(span.StartNs)/1e6, float64(span.LengthNs)/1e6, span.Kind)
			}

			renderSpan(img, r, root.Root.StartNs, span, label)

			r.y += imgBarHeight
		})
		if imgSeparatorFill != "" {
			img.Rect(0, r.y, r.widthPx, imgGap, fmt.Sprintf("fill=%q", imgSeparatorFill))
		}
		r.y += imgGap
	}

	img.End()
	return buf.Bytes()
}

func renderSummary(img *svg.SVG, r *Ribbon, x0 int64, span *cluster.Span) {
	offsetNs := span.StartNs - x0
	// Basic bar
	x := renderBar(img, r, x0, &Bar{
		offsetNs: offsetNs,
		lengthNs: span.LengthNs,
		minWidth: 1,
		style:    []string{fmt.Sprintf("fill=%q", imgBarFillBug)},
	})

	// TODO: update summary to include all wait/assist reasons

	// Color block times
	run, net, runSum := cluster.AllRunning(span)
	for _, v := range run {
		renderBar(img, r, x0, &Bar{
			offsetNs: offsetNs + v[0],
			lengthNs: v[1] - v[0],
			minWidth: 1,
			style:    []string{fmt.Sprintf("fill=%q", imgBarFillRunning)},
		})
	}
	for _, v := range net {
		renderBar(img, r, x0, &Bar{
			offsetNs: offsetNs + v[0],
			lengthNs: v[1] - v[0],
			style:    []string{fmt.Sprintf("fill=%q", imgBarFillNetwork)},
		})
	}

	label := fmt.Sprintf("g=%d @%0.6fms / %0.6fms / on CPU %0.6fms %s", span.G,
		float64(span.StartNs)/1e6, float64(span.LengthNs)/1e6, float64(runSum)/1e6, span.Kind)
	if imgTextSize != "" {
		// Text label
		textAttrs := []string{
			fmt.Sprintf("font-size=%q", imgTextSize),
			fmt.Sprintf("fill=%q", imgTextColor),
		}
		offsetSign := 1
		if x > imgWidth/2 {
			textAttrs = append(textAttrs, `text-anchor="end"`)
			offsetSign = -1
		}
		if x > imgWidth {
			x = imgWidth
		}
		img.Text(x+imgTextDx*offsetSign, r.y+imgTextDy, label, textAttrs...)
	}
}

func renderSpan(img *svg.SVG, r *Ribbon, x0 int64, span *cluster.Span, label string) {
	offsetNs := span.StartNs - x0
	// Basic bar
	x := renderBar(img, r, x0, &Bar{
		offsetNs: offsetNs,
		lengthNs: span.LengthNs,
		minWidth: 1,
		style:    []string{fmt.Sprintf("fill=%q", imgBarFillBug)},
	})

	// Color block times
	ranges, err := cluster.Running(span)
	if err != nil {
		return
	}

	addBlocks := func(segments [][2]int64, minWidth int, style []string) {
		for _, v := range segments {
			renderBar(img, r, x0, &Bar{
				offsetNs: offsetNs + v[0],
				lengthNs: v[1] - v[0],
				minWidth: minWidth,
				style:    style,
			})
		}
	}

	addBlocks(ranges.Running, 1, []string{fmt.Sprintf("fill=%q", imgBarFillRunning)})

	for reason, segments := range ranges.Assisting {
		style, ok := map[string][]string{
			"gc": {fmt.Sprintf("fill=%q", "chartreuse")},
		}[reason]
		if !ok {
			style = []string{fmt.Sprintf("fill=%q", imgBarFillBug)}
		}
		addBlocks(segments, 0, style)
	}
	for reason, segments := range ranges.Waiting {
		style, ok := map[string][]string{
			"net": {fmt.Sprintf("fill=%q", imgBarFillNetwork)},

			"cpu": {fmt.Sprintf("fill=%q", imgBarFillWaitCPU)},

			"gc": {fmt.Sprintf("fill=%q", imgBarFillAssist)},

			"block": {fmt.Sprintf("fill=%q", imgBarFillBlocked)},
			"cond":  {fmt.Sprintf("fill=%q", imgBarFillBlocked)},
			"sync":  {fmt.Sprintf("fill=%q", imgBarFillBlocked)},

			"recv":   {fmt.Sprintf("fill=%q", imgBarFillBlocked)},
			"select": {fmt.Sprintf("fill=%q", imgBarFillBlocked)},
			"send":   {fmt.Sprintf("fill=%q", imgBarFillBlocked)},

			"syscall": {fmt.Sprintf("fill=%q", imgBarFillSyscall)},
		}[reason]
		if !ok {
			style = []string{fmt.Sprintf("fill=%q", imgBarFillBug)}
		}
		addBlocks(segments, 0, style)
	}

	if label != "" && imgTextSize != "" {
		// Text label
		textAttrs := []string{
			fmt.Sprintf("font-size=%q", imgTextSize),
			fmt.Sprintf("fill=%q", imgTextColor),
		}
		offsetSign := 1
		if x > imgWidth/2 {
			textAttrs = append(textAttrs, `text-anchor="end"`)
			offsetSign = -1
		}
		if x > imgWidth {
			x = imgWidth
		}
		img.Text(x+imgTextDx*offsetSign, r.y+imgTextDy, label, textAttrs...)
	}
}

func renderBar(img *svg.SVG, r *Ribbon, x0 int64, b *Bar) (x int) {
	x = int(int64(r.widthPx) * b.offsetNs / r.widthNs)
	w := int(int64(r.widthPx)*(b.offsetNs+b.lengthNs)/r.widthNs) - x
	h := imgBarHeight
	if w < b.minWidth {
		w = b.minWidth
	}
	if w > 0 && x < r.widthPx {
		img.Rect(x, r.y, w, h, b.style...)
	}
	return x
}

type Ribbon struct {
	y       int
	widthNs int64
	widthPx int
}

type Bar struct {
	offsetNs int64
	lengthNs int64
	minWidth int
	style    []string
}
