package cluster

import (
	"reflect"
	"testing"
)

func TestCollapse(t *testing.T) {
	testcase := func(ranges [][2]int64, window [2]int64, want [][2]int64) func(t *testing.T) {
		return func(t *testing.T) {
			have := collapse(ranges, window)
			if !reflect.DeepEqual(have, want) {
				t.Errorf("collapse(%v, %v); %#v != %#v", ranges, window, have, want)
			}
		}
	}
	t.Run("", testcase(
		[][2]int64{{4, 7}},
		[2]int64{0, 10},
		[][2]int64{{4, 7}},
	))
	t.Run("", testcase(
		[][2]int64{{4, 7}},
		[2]int64{0, 6},
		[][2]int64{{4, 6}},
	))
	t.Run("", testcase(
		[][2]int64{{4, 7}},
		[2]int64{0, 3},
		nil,
	))
	t.Run("", testcase(
		[][2]int64{{4, 7}},
		[2]int64{0, 4},
		nil,
	))
	t.Run("", testcase(
		[][2]int64{{7, 4}},
		[2]int64{0, 10},
		nil,
	))
	t.Run("", testcase(
		[][2]int64{{14, 17}},
		[2]int64{0, 10},
		nil,
	))
	t.Run("", testcase(
		[][2]int64{{1, 4}, {4, 7}},
		[2]int64{0, 10},
		[][2]int64{{1, 7}},
	))
	t.Run("", testcase(
		[][2]int64{{1, 3}, {4, 7}},
		[2]int64{0, 10},
		[][2]int64{{1, 3}, {4, 7}},
	))

	t.Run("", testcase(
		[][2]int64{{4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}},
		[2]int64{0, 10},
		[][2]int64{{4, 7}},
	))
	t.Run("", testcase(
		[][2]int64{{4, 7}, {5, 7}, {6, 7}, {7, 7}, {8, 7}},
		[2]int64{0, 10},
		[][2]int64{{4, 7}},
	))

	t.Run("", testcase(
		[][2]int64{{4, 7}, {5, 8}},
		[2]int64{0, 10},
		[][2]int64{{4, 8}},
	))
	t.Run("", testcase(
		[][2]int64{{4, 7}, {5, 13}},
		[2]int64{0, 10},
		[][2]int64{{4, 10}},
	))
	t.Run("", testcase(
		[][2]int64{{4, 7}, {8, 13}},
		[2]int64{0, 10},
		[][2]int64{{4, 7}, {8, 10}},
	))
}

func TestSubtract(t *testing.T) {
	testcase := func(base [][2]int64, delta [][2]int64, want [][2]int64) func(t *testing.T) {
		return func(t *testing.T) {
			have := subtract(base, delta)
			if !reflect.DeepEqual(have, want) {
				t.Errorf("subtract(%v, %v); %#v != %#v", base, delta, have, want)
			}
		}
	}
	t.Run("", testcase(
		[][2]int64{{4, 7}},
		[][2]int64{{0, 10}},
		nil,
	))
	t.Run("", testcase(
		[][2]int64{{0, 10}},
		[][2]int64{{4, 7}},
		[][2]int64{{0, 4}, {7, 10}},
	))
	t.Run("", testcase(
		[][2]int64{{0, 10}},
		[][2]int64{{4, 17}},
		[][2]int64{{0, 4}},
	))
	t.Run("", testcase(
		[][2]int64{{0, 10}},
		[][2]int64{{14, 17}},
		[][2]int64{{0, 10}},
	))
	t.Run("", testcase(
		[][2]int64{{0, 10}, {20, 30}},
		[][2]int64{{5, 15}, {25, 35}},
		[][2]int64{{0, 5}, {20, 25}},
	))
	t.Run("", testcase(
		[][2]int64{{20, 30}, {0, 10}},
		[][2]int64{{5, 15}, {25, 35}},
		[][2]int64{{0, 5}, {20, 25}},
	))
	t.Run("", testcase(
		[][2]int64{{20, 30}, {0, 10}},
		[][2]int64{{25, 35}, {5, 15}},
		[][2]int64{{0, 5}, {20, 25}},
	))
}
