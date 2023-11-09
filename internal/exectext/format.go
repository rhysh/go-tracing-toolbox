package exectext

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

type EventWriter struct {
	Stack    bool
	Link     bool
	Backlink map[*trace.Event]*trace.Event
}

func (w *EventWriter) Format(ev *trace.Event) string {
	var links []string
	if from := w.Backlink[ev]; from != nil && ev.G != from.G {
		links = append(links, fmt.Sprintf("from %s", from))
	}
	if w.Link && ev.Link != nil && ev.G != ev.Link.G {
		links = append(links, fmt.Sprintf("to %s", ev.Link))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s", ev)
	if len(links) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(links, ", "))
	}
	fmt.Fprintf(&b, "\n")
	if w.Stack {
		for _, frame := range ev.Stk {
			fmt.Fprintf(&b, "  %x %s %s:%d\n", frame.PC, frame.Fn, frame.File, frame.Line)
		}
	}
	return b.String()
}

func ParseEvents(str string) ([]*trace.Event, error) {
	type evKey struct {
		Ts int64
		G  uint64
	}
	evm := make(map[evKey]*trace.Event)
	links := make(map[*trace.Event]evKey)

	var evs []*trace.Event
	for _, s := range splitEvents(str) {
		ev, to, err := parseEventString(s)
		if err != nil {
			return nil, err
		}
		evs = append(evs, ev)
		evm[evKey{Ts: ev.Ts, G: ev.G}] = ev

		if to != "" {
			evTo, _, err := parseEventTitle(to)
			if err != nil {
				return nil, fmt.Errorf("event %q links to %q: %w", s, to, err)
			}
			links[ev] = evKey{Ts: evTo.Ts, G: evTo.G}
		}
	}

	for from, toKey := range links {
		from.Link = evm[toKey]
	}

	// TODO: wire Link for self-links

	return evs, nil
}

func splitEvents(s string) []string {
	sc := bufio.NewScanner(strings.NewReader(s))
	var buf strings.Builder
	var evs []string
	flush := func() {
		ev := buf.String()
		buf.Reset()
		if ev != "" {
			evs = append(evs, ev)
		}
	}
	for sc.Scan() {
		if !strings.HasPrefix(sc.Text(), "  ") {
			flush()
		}
		if len(sc.Bytes()) > 0 {
			fmt.Fprintf(&buf, "%s\n", sc.Bytes())
		}
	}
	flush()
	return evs
}

func parseEventString(s string) (*trace.Event, string, error) {
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	title, stack := lines[0], lines[1:]
	ev, to, err := parseEventTitle(title)
	if err != nil {
		return nil, "", err
	}
	for _, line := range stack {
		frame, err := parseEventFrame(line)
		if err != nil {
			return ev, to, err
		}
		ev.Stk = append(ev.Stk, frame)
	}
	return ev, to, nil
}

func parseEventTitle(str string) (*trace.Event, string, error) {
	ev := &trace.Event{}
	parts := strings.SplitN(str, " ", 6)
	if len(parts) < 5 {
		return nil, "", fmt.Errorf("need at least five parts for event")
	}

	for i, desc := range trace.EventDescriptions {
		if parts[1] == desc.Name {
			ev.Type = uint8(i)
			break
		}
	}

	var err error

	trimPrefix := func(s, prefix string) string {
		if err != nil {
			return ""
		}
		v := strings.TrimPrefix(s, prefix)
		if len(s) != len(prefix)+len(v) {
			err = fmt.Errorf("missing %q prefix: %q", prefix, s)
		}
		return v
	}

	getInt := func(s string, base int, bitSize int) int64 {
		var i int64
		if err == nil {
			i, err = strconv.ParseInt(s, base, bitSize)
		}
		return i
	}

	getUint := func(s string, base int, bitSize int) uint64 {
		var i uint64
		if err == nil {
			i, err = strconv.ParseUint(s, base, bitSize)
		}
		return i
	}

	ev.Ts = getInt(parts[0], 10, 64)
	ev.P = int(getInt(trimPrefix(parts[2], "p="), 10, 0))
	ev.G = getUint(trimPrefix(parts[3], "g="), 10, 64)
	ev.Off = int(getInt(trimPrefix(parts[4], "off="), 10, 0))

	desc := trace.EventDescriptions[ev.Type]
	args := strings.Join(parts[5:], " ")
	for i, k := range desc.Args {
		parts := strings.SplitN(args, " ", 2)
		arg := parts[0]
		args = strings.Join(parts[1:], " ")
		if i <= len(ev.Args) {
			ev.Args[i] = getUint(trimPrefix(arg, k+"="), 10, 64)
		}
	}

	var to string
	if strings.HasPrefix(args, "(") && strings.HasSuffix(args, ")") {
		// EventTitle
		// EventTitle (to EventTitle)
		// EventTitle (from EventTitle)
		// EventTitle (from EventTitle, to EventTitle)
		inside := strings.TrimSuffix(strings.TrimPrefix(args, "("), ")")
		// Note: this does not do full recognition of the input language
		a, b, _ := strings.Cut(inside, ", ")
		if strings.HasPrefix(a, "to ") {
			to = strings.TrimPrefix(a, "to ")
		} else if strings.HasPrefix(b, "to ") {
			to = strings.TrimPrefix(b, "to ")
		}
	} else if args != "" {
		err = fmt.Errorf("extra args %q", args)
	}

	// TODO: string args (for EvUserLog and EvUserTaskCreate)

	if err != nil {
		return nil, "", fmt.Errorf("parseEventTitle %q: %w", str, err)
	}

	return ev, to, nil
}

func parseEventFrame(s string) (*trace.Frame, error) {
	frame := &trace.Frame{}
	parts := strings.SplitN(s, " ", 5)
	if len(parts) != 5 {
		return nil, fmt.Errorf("need five parts per frame")
	}

	if parts[0] != "" || parts[1] != "" {
		return nil, fmt.Errorf("need to start with two spaces")
	}

	pc, err := strconv.ParseInt(parts[2], 16, 64)
	if err != nil {
		return nil, fmt.Errorf("expect hex pc: %w", err)
	}
	frame.PC = uint64(pc)

	frame.Fn = parts[3]

	i := strings.LastIndex(parts[4], ":")
	if i < 0 {
		return nil, fmt.Errorf("no colon in file:line")
	}
	line, err := strconv.ParseInt(parts[4][i+len(":"):], 10, 0)
	if err != nil {
		return nil, fmt.Errorf("line number: %w", err)
	}
	frame.File = parts[4][:i]
	frame.Line = int(line)

	return frame, nil
}
