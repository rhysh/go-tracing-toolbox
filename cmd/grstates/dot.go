package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

func leftEscape(str string) string {
	parts := strings.Split(str, "\n")
	for i := range parts {
		parts[i] = strings.TrimPrefix(strings.TrimSuffix(fmt.Sprintf("%q", parts[i]), `"`), `"`)
	}
	return `"` + strings.Join(parts, "\\l") + `"`
}

func (g *vizGraph) writeDot(w io.Writer) error {
	var err error
	fprintf := func(format string, a ...any) {
		if err != nil {
			return
		}
		_, err = fmt.Fprintf(w, format, a...)
	}

	var (
		chartName   = "A"
		chartWidth  = 100
		chartHeight = 100
	)

	fprintf("digraph %s {\n", chartName)
	fprintf("\tsize=%q;\n", fmt.Sprintf("%d,%d", chartWidth, chartHeight))

	var nodeIDs []vizID
	for id := range g.nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return g.nodePriority[nodeIDs[i]] < g.nodePriority[nodeIDs[j]] })

	for _, id := range nodeIDs {
		node := g.nodes[id]
		var attrs []string
		for _, key := range []string{"shape", "label", "tooltip", "comment"} {
			attrs = append(attrs, fmt.Sprintf("%s=%s", key, node.attrs[key]))
		}
		fprintf("\t%s [%s];\n", id.Hash(), strings.Join(attrs, ","))
	}

	var edgeKeys [][2]vizID
	for ids := range g.edges {
		edgeKeys = append(edgeKeys, ids)
	}
	sort.Slice(edgeKeys, func(i, j int) bool {
		ki, kj := edgeKeys[i], edgeKeys[j]
		if ki[0] != kj[0] {
			return ki[0] < kj[0]
		}
		return ki[1] < kj[1]
	})

	for _, key := range edgeKeys {
		edge := g.edges[key]
		var attrs []string
		for _, key := range []string{"weight", "penwidth", "tooltip", "comment"} {
			attrs = append(attrs, fmt.Sprintf("%s=%s", key, edge.attrs[key]))
		}
		fprintf("\t%s -> %s [%s];\n", key[0].Hash(), key[1].Hash(), strings.Join(attrs, ","))
	}

	fprintf("}\n")

	return err
}
