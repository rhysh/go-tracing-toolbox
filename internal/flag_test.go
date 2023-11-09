package internal_test

import (
	"testing"

	"github.com/rhysh/go-tracing-toolbox/internal"
)

func TestFlag(t *testing.T) {
	goodcase := func(v string) func(t *testing.T) {
		return func(t *testing.T) {
			var f internal.StackFlag
			err := f.Set(v)
			if err != nil {
				t.Fatalf("StackFlag.Set(%q); err = %v", v, err)
			}
		}
	}

	roundtripcase := func(v string) func(t *testing.T) {
		return func(t *testing.T) {
			var f internal.StackFlag
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
			var f internal.StackFlag
			err := f.Set(v)
			if err == nil {
				t.Fatalf("StackFlag.Set(%q); err = nil", v)
			}
		}
	}

	t.Run("", badcase(""))
	t.Run("", badcase("Foo"))
	t.Run("", badcase("None"))

	t.Run("", goodcase("GoStart"))
	t.Run("", roundtripcase(`GoBlockRecv "**"`))
	t.Run("", roundtripcase(`GoBlockRecv "^net/http...conn..serve$" "**"`))
	t.Run("", roundtripcase(`Any "**" "^syscall.write$"`))

	t.Run("", badcase(`GoBlockRecv oops`))
	t.Run("", badcase(`GoBlockRecv "["`))
}
