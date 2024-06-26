# Go Tracing Toolbox

This project provides commands for bulk data analysis of Go profiles and execution traces.

Note that this does not yet support the v2 execution trace format.
Note that building most of these tools will first require copying in an execution trace parser.
(Much of this work predates the existence of importable parsers for Go execution tracers.)

```
go generate ./internal/_vendor/cp.go
```

Its contents range from "somewhat polished" to "raw and arcane".
Here they are in that order.

## The tools

### `apshuffle`

This tool is for use with profiling bundles generated by [Autoprof](https://pkg.go.dev/github.com/rhysh/autoprof).
If you have a big pile of them, this can help you find which ones describe a particular problem you'd like to understand.
Or, it can help you to see an overview of various aspects of your app's behavior.

#### "My HTTP server got slow -- what caused the requests to pile up?"

List the 30 profile bundles that show the largest number of concurrent inbound HTTP requests:

```
apshuffle -profile-sort=goroutine -focus=ServeHTTP | head -n 30
```

Then examine any outliers in detail with your usual `go tool pprof` workflow.
The first column is the path to the profile bundle, and the second column is the number of samples that match the filter.

Note that the `-focus` flag can be provided multiple times, filtering to only include the samples that have all of the provided regexps somewhere on the call stack.
(The `-ignore` flag can only be provided once.)

```
apshuffle -profile-sort=goroutine -focus=ServeHTTP -focus=sync...Mutex..Lock | head -n 30
```

#### "My app's live heap started growing (quickly)"

Look at the in-use heap, comparing it against the previous profile from the same process.

```
apshuffle -profile-sort=heap -sample-type=inuse_space -vs-prev | head -n 30
```

When you're looking at the behavior of a particular process over time, you may also want the tool to write "./_link/prev" and "./_link/next" symlinks to that instance's other profile bundles for use with the `-base` flag of `go tool pprof`.

```
apshuffle -write-links
```

#### "I'd like to see execution traces from when the app is at its busiest"

```
apshuffle -profile-sort=profile -with-trace | head -n 30
```

### `grstates`

This tool creates a visualization of the state machines that a program's goroutines run through in an execution trace.
It requires the help of the "dot" command, the same tool that "go tool pprof" uses for most of its visualizations.

It works with the v2 execution trace format, so supports execution traces from Go 1.22+.

```
grstates -input=./pprof/trace -dot=/tmp/trace.dot
dot -o/tmp/trace.svg -Tsvg /tmp/trace.dot
```

### `etgrep`

This tool gives a text-based peek into the data that make up Go's execution traces.
Its basic output is similar to that of `go tool trace -d`, but it also allows printing events' associated call stacks, and filtering based on event type and call stack structure.

```
etgrep -input=./pprof/trace -stacks | less
```

```
etgrep -input=./pprof/trace -match='GoBlockSync "net/http...conn..serve" "ServeHTTP" "**" "sync...Mutex..Lock"' | less
```

### `regiongraph`

This tool looks at the sequence of events and call stacks for each goroutine in an execution trace, searching for "regions" where a goroutine is doing a particular kind of work.
Those include "handling an inbound HTTP/1.x request", "orchestrating an outbound HTTP/1.x request", "doing a DNS lookup for an outbound HTTP request", "dialing a new connection for an outbound HTTP request", and a few others.
(It's somewhat straightforward to write new matchers and recompile the tool, but some day it would be nice to allow these to be specified at run-time.)

You can use the `runtime/trace` package to emit explicit events for the start and end of regions that are important to your app.
But sometimes it's not practical to add run-time instrumentation to SDKs you don't own (such as the internals of the `net/http` client), or you don't know until after the fact which parts of the app you'd like to investigate.
So this is in effect a way to add instrumentation afterwards.

```
regiongraph -input=./pprof/trace -show-regions | less
```

It can also calculate the causal relationships between regions: the reason that the `net/http` needs to do a DNS lookup is because it needs to dial a new outbound connection, and the reason for that is the application making an outbound HTTP request, and the reason for that might be that the application received an _inbound_ HTTP request.

On a good day, this tool can attribute the bulk of an HTTP server process's work -- spread across many goroutines -- to particular inbound requests.
It can write out those results as a bunch of lines of JSON, one per "cause".
The result can be analyzed directly (use `jq` or similar to identify "slow" requests, or those that include a lot of on-CPU time), or fed into a tool like `etviz` (see below).

```
regiongraph -input=./pprof/trace -json -summarize > /tmp/regions.json
```

Maybe you have a few hundred execution traces and you'd like to see which of them include examples of your program's worst behavior.
For an HTTP server, that might be the 99.9th percentile of slowest requests.
Then you can open a UI like `go tool trace` or [gotraceui](https://gotraceui.dev) on the execution trace you found, and immediately focus on the right time range and goroutines.

### `etviz`

This generates an SVG to visualize regions.
The style comes from Richard L. Sites' book _Understanding Software Dynamics_.

Assuming that you're analyzing an HTTP server, it helps if you first filter to the regions that are rooted at inbound HTTP requests.
The image then shows each request in its own horizontal band; the requests are ordered vertically in the same way they were in the input file, and the horizontal axis is time.
It shows the full set of requests twice: first with their horizontal starting positions offset based on when they started (easy to see arrival rate and concurrency), and then with all of their starting positions fixed at zero (easy to compare durations).

Each band is colored based on what the goroutine(s) are doing. On-CPU time ("running") is black, scheduler wait time ("runnable") is grey, GC assist is chartreuse, network wait is yellow, other forms of blocking are magenta.
When a band is summarizing the state of multiple concurrent goroutines, it has to prioritize which color to show; if a goroutine is waiting on the network we won't see others blocking on channels, and if one of the goroutines is running then we don't mind that others may be waiting on the network.

Usually you'll see the collapsed view, with one band per inbound HTTP request (or other top-level source of work).
For diving into a small number of requests (maybe only those on a particular goroutine, or in a very small time range), you can add the `-details` flag to see a separate line for each goroutine and region the tools identified as being involved.

```
etviz -input=/tmp/regions.json -output=/tmp/requests.svg
chrome /tmp/requests.svg
```

### `scope_chart` and `scope_tls`

These are a kind of wild idea about how to take advantage of the CPU profile samples that can appear -- with timestamps! -- in execution traces.

Say there's a part of our app that takes about two milliseconds to run, and we're trying to understand what it's doing in more detail.
Go's CPU profiles include 100 samples per second of on-CPU time; each sample represents 10 milliseconds of work.
That means for every five times our app executes the two-millisecond-long function of interest, we can expect to get a single CPU profile sample.

My hypothesis here is that if we're willing to make certain assumptions about how repetitive the app's work is, we can build up a time series of CPU profile samples with much higher resolution than 10 milliseconds -- similar to how a sampling oscilloscope works.

Maybe we find an example of the region of interest taking 2.1 ms, with a CPU profile sample at 0.7 ms after the start.
The next time we see it (maybe from a different execution trace, collected from a different machine), it took 1.8 ms and the CPU profile sample arrived at 0.9 ms.
Then another taking 2.0 ms with a sample at 1.5 ms.
We can render these as marks at "33%", "50%", and "75%" respectively.

With a few dozen or hundred of them we can start to tell a pretty clear story of what that code is doing and when.
Of course to do so we need to make the assumption that the work is pretty consistent over time.
But the other data available in the execution trace can allow some advanced filtering, to better identify which units of work are truly similar to each other.
That might be TLS handshakes only for particular dependencies.
Or it might be the work of unmarshaling inbound requests, but only for requests that result in a specific outbound call (too late to add a `runtime/pprof` label).

This pair of tools is a rough draft of that idea.
