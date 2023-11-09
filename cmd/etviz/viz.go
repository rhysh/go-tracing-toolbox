package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"os"

	"github.com/rhysh/go-tracing-toolbox/internal/cluster"
	"github.com/rhysh/go-tracing-toolbox/internal/viz"
)

func main() {
	input := flag.String("input", "", "Path to JSON lines (from regiongraph, subject to change)")
	output := flag.String("output", "", "Path to SVG output file")
	details := flag.Bool("details", false, "Show details of the goroutines involved in each cluster")
	flag.Parse()

	inFile, err := os.Open(*input)
	if err != nil {
		log.Printf("Open: %v", err)
	}
	defer inFile.Close()

	dec := json.NewDecoder(inFile)
	var summaries []*cluster.TreeSummary
	for {
		var summary cluster.TreeSummary
		err := dec.Decode(&summary)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("json.Unmarshal: %v", err)
		}

		reject := func(span *cluster.Span) bool {
			return span.Kind == "net/http.(*connReader).backgroundRead"
		}
		cluster.Visit(summary.Root, func(span *cluster.Span) {
			caused := span.Caused
			span.Caused = nil
			for _, sp := range caused {
				if reject(sp) {
					continue
				}
				span.Caused = append(span.Caused, sp)
			}
		})
		if reject(summary.Root) {
			continue
		}

		summaries = append(summaries, &summary)
	}

	outFile, err := os.Create(*output)
	if err != nil {
		log.Fatalf("Create: %v", err)
	}
	defer func() {
		err := outFile.Close()
		if err != nil {
			log.Fatalf("Close: %v", err)
		}
	}()

	buf := viz.Render(summaries, *details)
	_, err = outFile.Write(buf)
	if err != nil {
		log.Fatalf("Write: %v", err)
	}
}
