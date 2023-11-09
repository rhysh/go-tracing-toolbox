package internal_test

import (
	"testing"
	"time"

	"github.com/rhysh/go-tracing-toolbox/internal"
)

func TestUncovered(t *testing.T) {
	testcase := func(want time.Duration, parent internal.TimeSpan, children ...internal.TimeSpan) func(t *testing.T) {
		return func(t *testing.T) {
			have := internal.Uncovered(parent, children...)
			if have != want {
				t.Errorf("Uncovered(%v, %v); %d != %d", parent, children, have, want)
			}
		}
	}

	t.Run("", testcase(100, internal.TimeSpan{0, 100}))
	t.Run("", testcase(70, internal.TimeSpan{30, 100}))
	t.Run("", testcase(20, internal.TimeSpan{30, 100},
		internal.TimeSpan{30, 40}, internal.TimeSpan{50, 80}, internal.TimeSpan{60, 70}, internal.TimeSpan{70, 90}))
}
