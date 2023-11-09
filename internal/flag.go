package internal

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

type StackFlag struct {
	Event byte
	Specs []string
}

func (sf *StackFlag) EventMatches(t byte) bool {
	return sf.Event == 0xFF || sf.Event == t
}

var _ flag.Value = (*StackFlag)(nil)

func (sf *StackFlag) String() string {
	if sf == nil {
		return "<nil>"
	}

	var buf strings.Builder

	name := ""
	if sf.Event == 0xFF {
		name = "Any"
	} else {
		name = trace.EventDescriptions[sf.Event].Name
	}
	fmt.Fprintf(&buf, "%s", name)
	for _, fn := range sf.Specs {
		fmt.Fprintf(&buf, " %q", fn)
	}
	if len(sf.Specs) == 0 {
		fmt.Fprintf(&buf, " **")
	}
	return buf.String()
}

func (sf *StackFlag) Set(v string) error {
	sf.Event = 0
	sf.Specs = nil

	parts := strings.SplitN(v, " ", 2)
	for id, desc := range trace.EventDescriptions {
		if desc.Name == parts[0] {
			sf.Event = byte(id)
			break
		}
	}
	if parts[0] == "Any" {
		sf.Event = 0xFF
	}
	if sf.Event == 0 {
		return fmt.Errorf("invalid trace event name %q", parts[0])
	}

	if len(parts) == 1 {
		return nil
	}
	specs := strings.NewReader(parts[1])
	for {
		var s string
		_, err := fmt.Fscanf(specs, "%q", &s)
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		sf.Specs = append(sf.Specs, s)
	}

	// Verify the stack-matching regular expressions (and memoize the compiled regexps)
	_, err := globalProgram.hasStackRe(nil, sf.Specs...)
	if err != nil {
		return fmt.Errorf("invalid stack matcher flag: %w", err)
	}
	return nil
}
