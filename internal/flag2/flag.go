package flag2

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/rhysh/go-tracing-toolbox/internal/match2"
	"golang.org/x/exp/trace"
)

type StackFlag struct {
	Event trace.EventKind // Use 0 ("EventBad") to match everything
	Specs []string
}

func (sf *StackFlag) EventMatches(t trace.EventKind) bool {
	return sf.Event == trace.EventBad || sf.Event == t
}

var _ flag.Value = (*StackFlag)(nil)

func (sf *StackFlag) String() string {
	if sf == nil {
		return "<nil>"
	}

	var buf strings.Builder

	name := ""
	if sf.Event == trace.EventBad {
		name = "Any"
	} else {
		name = sf.Event.String()
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
	sf.Event = trace.EventBad
	sf.Specs = nil

	parts := strings.SplitN(v, " ", 2)
	for kind := trace.EventKind(1); kind != trace.EventBad; kind++ {
		if kind.String() == trace.EventBad.String() {
			break
		}
		if kind.String() == parts[0] {
			sf.Event = kind
			break
		}
	}
	if sf.Event == trace.EventBad {
		if parts[0] == "Any" {
			// leave as 0 / EventBad
		} else {
			return fmt.Errorf("invalid trace event name %q", parts[0])
		}
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
	err := match2.ValidateRe(sf.Specs...)
	if err != nil {
		return fmt.Errorf("invalid stack matcher flag: %w", err)
	}
	return nil
}
