package internal_test

import (
	"reflect"
	"testing"

	"github.com/rhysh/go-tracing-toolbox/internal"
	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func TestHasStackRe(t *testing.T) {
	stack := []*trace.Frame{
		&trace.Frame{Fn: "e"},
		&trace.Frame{Fn: "x/vendor/d"},
		&trace.Frame{Fn: "cee"},
		&trace.Frame{Fn: "bee"},
		&trace.Frame{Fn: "a"},
	}

	testcase := func(want bool, specs ...string) func(t *testing.T) {
		return func(t *testing.T) {
			have := internal.HasStackRe(stack, specs...)
			if have != want {
				t.Errorf("HasStackRe(%q); %t != %t", specs, have, want)
			}
		}
	}

	t.Run("", func(t *testing.T) {
		match := internal.HasStackRe(nil, "**")
		if !match {
			t.Errorf("HasStackRe(nil, \"**\") should match")
		}
	})

	t.Run("", func(t *testing.T) {
		match := internal.HasStackRe(nil, "**", "f", "**")
		if match {
			t.Errorf("HasStackRe(nil, \"**\" \"f\" \"**\") should not match")
		}
	})

	t.Run("", testcase(true, "**"))
	t.Run("", testcase(true, `a`, "**"))
	t.Run("", testcase(false, `bee`, "**"))
	t.Run("", testcase(true, "**", `bee`, "**"))
	t.Run("", testcase(true, "**", `e`))
	t.Run("", testcase(true, "**", `e`, "**"))
	t.Run("", testcase(true, "**", `a`, "**"))
	t.Run("", testcase(true, `a`, "**", `bee`, "**"))
	t.Run("", testcase(true, `a`, "**", "**", "**", `bee`, "**"))
	t.Run("", testcase(true, `a`, `ee`, `ee`, "**"))
	t.Run("", testcase(true, `a`, "**", `^d$`, "**"))
	t.Run("", testcase(false, `a`, ".*", `^d$`, ".*"))
	t.Run("", testcase(true, `a`, ".*", ".*", `^d$`, ".*"))
	t.Run("", testcase(true, `a`, "**", "**", "**", "**", "**", "**", `^d$`, "**"))
	t.Run("", testcase(false, "**", `x`, "**"))
}

func TestFindStackSubmatchIndex(t *testing.T) {
	stack := []*trace.Frame{
		{Fn: "github.com/twitchtv/twirp/example.(*haberdasherServer).serveMakeHatProtobuf.func1"},
		{Fn: "github.com/twitchtv/twirp/example.(*haberdasherServer).serveMakeHatProtobuf.func2"},
		{Fn: "github.com/twitchtv/twirp/example.(*haberdasherServer).serveMakeHatProtobuf"},
		{Fn: "github.com/twitchtv/twirp/example.(*haberdasherServer).serveMakeHat"},
		{Fn: "github.com/twitchtv/twirp/example.(*haberdasherServer).ServeHTTP"},
		{Fn: "net/http.HandlerFunc.ServeHTTP"},
		{Fn: "net/http.serverHandler.ServeHTTP"},
		{Fn: "net/http.(*conn).serve"},
	}

	testcase := func(want []int, specs ...string) func(t *testing.T) {
		return func(t *testing.T) {
			have := internal.FindStackSubmatchIndex(stack, specs...)
			if !reflect.DeepEqual(have, want) {
				t.Errorf("FindStackSubmatchIndex(%q); %d != %d", specs, have, want)
			}
		}
	}

	t.Run("", testcase([]int{ // "github.com/twitchtv/twirp/example.(*haberdasherServer).serveMakeHat"
		3, 0, 33, // "github.com/twitchtv/twirp/example"
		3, 36, 47, // "haberdasher"
		3, 60, 67, // "MakeHat"
	}, `**`, `\.ServeHTTP$`, `^(.*)\.\(\*([^\)]*)Server\)\.serve([^\./]*)$`, `**`))

	// The order of the triplets matches the order they appear in the specs
	t.Run("", testcase([]int{ // "github.com/twitchtv/twirp/example.(*haberdasherServer).serveMakeHatProtobuf"
		5, 0, 20, // "net/http.HandlerFunc"
		2, 60, 67, // "MakeHat"
	}, `**`, `^(.*)\.ServeHTTP$`, `\.ServeHTTP$`, `\.serve`, `\.serve([^\./]*)Protobuf$`, `**`))

}
