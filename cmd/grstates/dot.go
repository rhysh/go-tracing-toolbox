package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"golang.org/x/exp/trace"
)

func leftEscape(str string) string {
	parts := strings.Split(str, "\n")
	for i := range parts {
		parts[i] = strings.TrimPrefix(strings.TrimSuffix(fmt.Sprintf("%q", parts[i]), `"`), `"`)
	}
	return `"` + strings.Join(parts, "\\l") + `"`
}

func hash(str string) string {
	h := sha256.Sum256([]byte(str))
	return "h" + hex.EncodeToString(h[:])
}

func writeDot(w io.Writer, goroutines map[trace.GoID]*behaviors) error {
	var err error
	fprintf := func(format string, a ...any) {
		if err != nil {
			return
		}
		_, err = fmt.Fprintf(w, format, a...)
	}

	stackSet := newStackSet()

	var goids []trace.GoID
	for goid, _ := range goroutines {
		goids = append(goids, goid)
	}
	sort.Slice(goids, func(i, j int) bool { return goids[i] < goids[j] })

	allStacks := make(map[trace.Stack]int)
	initStacks := make(map[trace.Stack]int)
	finalStacks := make(map[trace.Stack]int)
	stackPairs := make(map[[2]trace.Stack]int)
	for _, goid := range goids {
		b := goroutines[goid]

		if stk := b.initStack; !(stk == trace.NoStack || stackIsBuggy(stk)) {
			initStacks[stk]++
		}

		for pair, count := range b.pairs {
			allStacks[pair[1]]++
			if pair[0] == trace.NoStack {
				continue
			}
			allStacks[pair[0]]++
			stackPairs[pair] += count
		}

		if b.exited && allStacks[b.prevStack] != 0 {
			finalStacks[b.prevStack]++
		}
	}

	var (
		chartName   = "A"
		chartWidth  = 100
		chartHeight = 100
	)

	fprintf("digraph %s {\n", chartName)
	fprintf("\tsize=%q;\n", fmt.Sprintf("%d,%d", chartWidth, chartHeight))

	initNodes := make(map[string]int)
	otherNodes := make(map[string]int)
	finalNodes := make(map[string]int)
	edges := make(map[[2]string]int)

	fprintf("\t/* goroutine init stacks */\n")
	for stk, count := range initStacks {
		str := stackSet.formatShort(stk)
		initNodes[str] += count
	}
	var initKeys []string
	for str := range initNodes {
		initKeys = append(initKeys, str)
	}
	sort.Slice(initKeys, func(i, j int) bool {
		ki, kj := initKeys[i], initKeys[j]
		ni, nj := initNodes[ki], initNodes[kj]
		if ni != nj {
			// More common cases sort higher
			return ni > nj
		}
		return ki < kj
	})
	for _, str := range initKeys {
		count := initNodes[str]
		fprintf("\t%s [shape=box,label=%s]; /* seen %d times */\n", hash(str), leftEscape(str), count)
	}

	fprintf("\t/* other known stacks */\n")
	for stk, count := range allStacks {
		if _, ok := initStacks[stk]; ok {
			continue
		}
		str := stackSet.formatShort(stk)
		otherNodes[str] += count
	}
	var otherKeys []string
	for str := range otherNodes {
		otherKeys = append(otherKeys, str)
	}
	sort.Slice(otherKeys, func(i, j int) bool {
		ki, kj := otherKeys[i], otherKeys[j]
		ni, nj := otherNodes[ki], otherNodes[kj]
		if ni != nj {
			// More common cases sort higher
			return ni > nj
		}
		return ki < kj
	})
	for _, str := range otherKeys {
		count := otherNodes[str]
		fprintf("\t%s [label=%s]; /* seen %d times */\n", hash(str), leftEscape(str), count)
	}

	fprintf("\t/* edges */\n")
	maxCount := 1
	for pair, count := range stackPairs {
		edges[[2]string{stackSet.formatShort(pair[0]), stackSet.formatShort(pair[1])}] += count
		if count > maxCount {
			maxCount = count
		}
	}
	penwidth := func(count int) float64 {
		const maxWidth = 5
		return (maxWidth-1)*(math.Log(float64(count))/math.Log(float64(maxCount))) + 1
	}
	var edgeKeys [][2]string
	for strs := range edges {
		edgeKeys = append(edgeKeys, strs)
	}
	sort.Slice(edgeKeys, func(i, j int) bool {
		ki, kj := edgeKeys[i], edgeKeys[j]
		if ki[0] != kj[0] {
			return ki[0] < kj[0]
		}
		return ki[1] < kj[1]
	})
	for _, strs := range edgeKeys {
		count := edges[strs]
		fprintf("\t%s -> %s [weight=%d,penwidth=%f];\n", hash(strs[0]), hash(strs[1]), count,
			penwidth(count))
	}

	fprintf("\t/* end stacks */\n")
	for stk, count := range finalStacks {
		str := stackSet.formatShort(stk)
		finalNodes[str] += count
	}
	var finalKeys []string
	for str := range finalNodes {
		finalKeys = append(finalKeys, str)
	}
	sort.Slice(finalKeys, func(i, j int) bool {
		ki, kj := finalKeys[i], finalKeys[j]
		ni, nj := finalNodes[ki], finalNodes[kj]
		if ni != nj {
			// More common cases sort higher
			return ni > nj
		}
		return ki < kj
	})
	for _, str := range finalKeys {
		count := finalNodes[str]
		from := hash(str)
		to := "EXIT_" + from
		fprintf("\t%s [shape=box,label=%q]; /* seen %d times */\n", to, "EXIT", count)
		fprintf("\t%s -> %s [weight=%d,penwidth=%f];\n", from, to, count,
			penwidth(count))
	}

	fprintf("}\n")

	return nil
}
