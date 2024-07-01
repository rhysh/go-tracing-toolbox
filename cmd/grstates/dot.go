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

func writeDot(w io.Writer, goroutines map[trace.GoID]*behaviors, why *examples) error {
	var err error
	fprintf := func(format string, a ...any) {
		if err != nil {
			return
		}
		_, err = fmt.Fprintf(w, format, a...)
	}

	stackSet := newStackSet()

	formatShort := func(state stackState) string {
		return fmt.Sprintf("%s\n%s", state.state, stackSet.formatShort(state.stack))
	}

	var goids []trace.GoID
	for goid, _ := range goroutines {
		goids = append(goids, goid)
	}
	sort.Slice(goids, func(i, j int) bool { return goids[i] < goids[j] })

	whyStrings := make(map[string]string)
	whyState := func(ss stackState) {
		name := formatShort(ss)
		reason, _, _ := strings.Cut(why.stackState[ss].String(), "\n")
		whyStrings[name] = reason
	}
	whyEdge := func(e edge) {
		name := fmt.Sprintf("%s -> %s", hash(formatShort(e.from)), hash(formatShort(e.to)))
		reason, _, _ := strings.Cut(why.edgeTo[e].String(), "\n")
		whyStrings[name] = reason
	}

	allStates := make(map[stackState]int)
	initStates := make(map[stackState]int)
	finalStates := make(map[stackState]int)
	statePairs := make(map[[2]stackState]int)
	for _, goid := range goids {
		b := goroutines[goid]

		for edge, count := range b.edges {
			if edge.from.stack == trace.NoStack {
				continue
			}
			if edge.from.state == trace.GoNotExist {
				initStates[edge.to]++
				continue
			}
			if edge.to.state == trace.GoNotExist {
				finalStates[edge.from]++
				continue
			}
			allStates[edge.to]++
			allStates[edge.from]++
			statePairs[[2]stackState{edge.from, edge.to}] += count

			whyState(edge.to)
			whyState(edge.from)
			whyEdge(edge)
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
	for state, count := range initStates {
		str := formatShort(state)
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
	for _, key := range initKeys {
		count := initNodes[key]
		label := fmt.Sprintf("%s\n%s", "New goroutine", key)
		fprintf("\t%s [label=%s]; /* seen %d times */\n", hash(key), leftEscape(label), count)
	}

	fprintf("\t/* other known stacks */\n")
	for state, count := range allStates {
		if _, ok := initStates[state]; ok {
			continue
		}
		str := formatShort(state)
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
		tooltip := fmt.Sprintf("example: %s", whyStrings[str])
		fprintf("\t%s [shape=box,label=%s,tooltip=%q,comment=%q];\n", hash(str), leftEscape(str), tooltip, tooltip)
	}

	fprintf("\t/* edges */\n")
	maxCount := 1
	for pair, count := range statePairs {
		edges[[2]string{formatShort(pair[0]), formatShort(pair[1])}] += count
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
		name := fmt.Sprintf("%s -> %s", hash(strs[0]), hash(strs[1]))
		tooltip := fmt.Sprintf("example: %s", whyStrings[name])
		fprintf("\t%s [weight=%d,penwidth=%f,tooltip=%q,comment=%q];\n", name, count,
			penwidth(count), tooltip, tooltip)
	}

	fprintf("\t/* end stacks */\n")
	for state, count := range finalStates {
		str := formatShort(state)
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
		fprintf("\t%s [label=%q];\n", to, "EXIT")
		fprintf("\t%s -> %s [weight=%d,penwidth=%f];\n", from, to, count,
			penwidth(count))
	}

	fprintf("}\n")

	return nil
}
