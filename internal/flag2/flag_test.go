package flag2_test

import (
	"testing"

	"github.com/rhysh/go-tracing-toolbox/internal/flag2"
)

func TestFlag(t *testing.T) {
	goodcase := func(v string) func(t *testing.T) {
		return func(t *testing.T) {
			var f flag2.StackFlag
			err := f.Set(v)
			if err != nil {
				t.Fatalf("StackFlag.Set(%q); err = %v", v, err)
			}
		}
	}

	roundtripcase := func(v string) func(t *testing.T) {
		return func(t *testing.T) {
			var f flag2.StackFlag
			err := f.Set(v)
			if err != nil {
				t.Fatalf("StackFlag.Set(%q); err = %v", v, err)
			}
			if s := f.String(); v != s {
				t.Errorf("StackFlag.Set(%q).String() != %q", v, s)
			}
		}
	}

	badcase := func(v string) func(t *testing.T) {
		return func(t *testing.T) {
			var f flag2.StackFlag
			err := f.Set(v)
			if err == nil {
				t.Fatalf("StackFlag.Set(%q); err = nil", v)
			}
		}
	}

	t.Run("", badcase(""))
	t.Run("", badcase("Foo"))
	t.Run("", badcase("None"))

	t.Run("", goodcase("Metric"))
	t.Run("", roundtripcase(`StateTransition "**"`))
	t.Run("", roundtripcase(`StateTransition "^net/http...conn..serve$" "**"`))
	t.Run("", roundtripcase(`Any "**" "^syscall.write$"`))

	t.Run("", badcase(`StateTransition oops`))
	t.Run("", badcase(`StateTransition "["`))
}
