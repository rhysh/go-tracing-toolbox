package exectext_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rhysh/go-tracing-toolbox/internal/exectext"
)

func TestParse(t *testing.T) {
	str := `
17 GoStart p=0 g=42 off=1 g=42 seq=0
18 GoBlockSelect p=0 g=42 off=2
  abc123 runtime.selectgo /usr/local/go/src/runtime/select.go:1
  10face pkg.execute /home/gopher/src/pkg/somewhere.go:2
19 GoStart p=0 g=100 off=3 g=100 seq=0
20 GoUnblock p=0 g=100 off=4 g=42 seq=0 (to 22 GoStart p=0 g=42 off=6 g=42 seq=0)
21 GoEnd p=0 g=100 off=5
22 GoStart p=0 g=42 off=6 g=42 seq=0
`[1:]

	evs, err := exectext.ParseEvents(str)
	if err != nil {
		t.Fatalf("ParseEvents; err = %v", err)
	}
	w1 := exectext.EventWriter{Stack: true, Link: true}
	w2 := exectext.EventWriter{Stack: true}
	var all1, all2, all2b strings.Builder
	for _, ev := range evs {
		s := w1.Format(ev)
		fmt.Fprintf(&all1, "%s", s)
		fmt.Fprintf(&all2, "%s", w2.Format(ev))

		e, err := exectext.ParseEvents(s)
		if err != nil {
			t.Errorf("ParseEvents(%q); err = %v", s, err)
			continue
		}
		if len(e) != 1 {
			t.Errorf("ParseEvents(%q); len = %d", s, len(e))
			continue
		}

		fmt.Fprintf(&all2b, "%s", w2.Format(e[0]))
	}

	if have, want := all2.String(), all2b.String(); have != want {
		t.Errorf("Failed round trip;\n%s\n!=\n%s", have, want)
	}

	if have, want := all1.String(), str; have != want {
		t.Errorf("Failed round trip with Link enabled;\n%s\n!=\n%s", have, want)
	}
}
