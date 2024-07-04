package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"

	"golang.org/x/exp/trace"
)

func buildGraph(goroutines map[trace.GoID]*behaviors, why *examples) *vizGraph {
	graph := newVizGraph()

	stackSet := newStackSet()

	formatShort := func(state stackState) string {
		return fmt.Sprintf("%s\n%s", state.state, stackSet.formatShort(state.stack))
	}

	var goids []trace.GoID
	for goid, _ := range goroutines {
		goids = append(goids, goid)
	}
	sort.Slice(goids, func(i, j int) bool { return goids[i] < goids[j] })

	allStates := make(map[stackState]int)
	initStates := make(map[stackState]int)
	initWhy := make(map[stackState]trace.Event)
	finalStates := make(map[stackState]int)
	finalWhy := make(map[stackState]trace.Event)
	edges := make(map[edge]int)
	for _, goid := range goids {
		b := goroutines[goid]

		for e, count := range b.edges {
			if e.from.stack == trace.NoStack {
				continue
			}
			if e.from.state == trace.GoNotExist {
				initStates[e.to]++
				initWhy[e.to] = why.stackState[e.from]
				continue
			}
			if e.to.state == trace.GoNotExist {
				finalStates[e.from]++
				finalWhy[e.from] = why.edgeTo[e]
				continue
			}
			allStates[e.to]++
			allStates[e.from]++

			simpleEdge := edge{from: e.from, to: e.to} // ignore "via"
			edges[simpleEdge] += count
		}
	}

	var maxWeight int
	for _, count := range edges {
		if count > maxWeight {
			maxWeight = count
		}
	}
	penwidth := func(count int) float64 {
		const maxWidth = 5
		return (maxWidth-1)*(math.Log(float64(count))/math.Log(float64(maxWeight))) + 1
	}

	for edge, count := range edges {
		reason, _, _ := strings.Cut(why.edgeTo[edge].String(), "\n")
		tooltip := fmt.Sprintf("%q", "example: "+reason)

		e := newVizEdge(vizID(formatShort(edge.from)), vizID(formatShort(edge.to)))
		e.attrs["weight"] = fmt.Sprintf("%d", count)
		e.attrs["penwidth"] = fmt.Sprintf("%f", penwidth(count))
		e.attrs["tooltip"] = tooltip
		e.attrs["comment"] = tooltip
		graph.addEdge(e)
	}

	// goroutine launch states
	initCount := make(map[vizID]int)
	for ss, count := range initStates {
		key := vizID(formatShort(ss))
		label := fmt.Sprintf("%s\n%s", "New goroutine", formatShort(ss))

		reason, _, _ := strings.Cut(initWhy[ss].String(), "\n")
		tooltip := fmt.Sprintf("%q", reason)

		node := newVizNode(key)
		node.attrs["label"] = leftEscape(label)
		node.attrs["tooltip"] = tooltip
		node.attrs["comment"] = tooltip
		node.attrs["shape"] = "ellipse"
		graph.addNode(node)

		initCount[key] = count
	}
	var initPrio []vizID
	for key := range initCount {
		initPrio = append(initPrio, key)
	}
	sort.Slice(initPrio, func(i, j int) bool {
		ki, kj := initPrio[i], initPrio[j]
		ni, nj := initCount[ki], initCount[kj]
		if ni != nj {
			// More common cases sort higher
			return ni > nj
		}
		return ki < kj
	})
	for _, key := range initPrio {
		graph.nodePriority[key] = len(graph.nodePriority) + 1
	}

	// goroutine non-initial states
	allCount := make(map[vizID]int)
	for ss, count := range allStates {
		if _, ok := initStates[ss]; ok {
			continue
		}

		key := vizID(formatShort(ss))
		label := formatShort(ss)
		reason, _, _ := strings.Cut(why.stackState[ss].String(), "\n")
		tooltip := fmt.Sprintf("%q", "example: "+reason)

		node := newVizNode(key)
		node.attrs["label"] = leftEscape(label)
		node.attrs["tooltip"] = tooltip
		node.attrs["comment"] = tooltip
		node.attrs["shape"] = "box"
		graph.addNode(node)

		allCount[key] = count
	}
	var allPrio []vizID
	for key := range allCount {
		allPrio = append(allPrio, key)
	}
	sort.Slice(allPrio, func(i, j int) bool {
		ki, kj := allPrio[i], allPrio[j]
		ni, nj := allCount[ki], allCount[kj]
		if ni != nj {
			// More common cases sort higher
			return ni > nj
		}
		return ki < kj
	})
	for _, key := range allPrio {
		graph.nodePriority[key] = len(graph.nodePriority) + 1
	}

	// goroutine final states
	exitCount := make(map[vizID]int)
	for ss, count := range finalStates {
		fromKey := vizID(formatShort(ss))
		exitKey := vizID("EXIT_" + formatShort(ss))
		label := "EXIT"
		reason, _, _ := strings.Cut(finalWhy[ss].String(), "\n")
		tooltip := fmt.Sprintf("%q", "example: "+reason)

		node := newVizNode(exitKey)
		node.attrs["label"] = leftEscape(label)
		node.attrs["tooltip"] = tooltip
		node.attrs["comment"] = tooltip
		node.attrs["shape"] = "ellipse"
		graph.addNode(node)

		e := newVizEdge(fromKey, exitKey)
		e.attrs["weight"] = fmt.Sprintf("%d", count)
		e.attrs["penwidth"] = fmt.Sprintf("%f", penwidth(count))
		e.attrs["tooltip"] = tooltip
		e.attrs["comment"] = tooltip
		graph.addEdge(e)

		exitCount[exitKey] = count
	}
	var exitPrio []vizID
	for key := range exitCount {
		exitPrio = append(exitPrio, key)
	}
	sort.Slice(exitPrio, func(i, j int) bool {
		ki, kj := exitPrio[i], exitPrio[j]
		ni, nj := allCount[ki], allCount[kj]
		if ni != nj {
			// More common cases sort higher
			return ni > nj
		}
		return ki < kj
	})
	for _, key := range exitPrio {
		graph.nodePriority[key] = len(graph.nodePriority) + 1
	}

	return graph
}

type vizID string

func (id vizID) Hash() string {
	h := sha256.Sum256([]byte(id))
	return "h" + hex.EncodeToString(h[:])
}

type vizNode struct {
	id    vizID
	attrs map[string]string
}

type vizEdge struct {
	from  vizID
	to    vizID
	attrs map[string]string
}

type vizGraph struct {
	nodes        map[vizID]vizNode
	edges        map[[2]vizID]vizEdge
	nodePriority map[vizID]int
}

func newVizGraph() *vizGraph {
	return &vizGraph{
		nodes:        make(map[vizID]vizNode),
		edges:        make(map[[2]vizID]vizEdge),
		nodePriority: make(map[vizID]int),
	}
}

func newVizNode(id vizID) vizNode {
	return vizNode{id: id, attrs: make(map[string]string)}
}

func newVizEdge(from, to vizID) vizEdge {
	return vizEdge{from: from, to: to, attrs: make(map[string]string)}
}

func (g *vizGraph) addNode(n vizNode) {
	g.nodes[n.id] = n
}

func (g *vizGraph) addEdge(e vizEdge) {
	g.edges[[2]vizID{e.from, e.to}] = e
}
