package internal

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/rhysh/go-tracing-toolbox/internal/_vendor/trace"
)

func HasStackRe(stk []*trace.Frame, specs ...string) bool {
	match, err := globalProgram.hasStackRe(stk, specs...)
	if err != nil {
		panic(err)
	}
	return match != nil
}

func TrimVendor(fn string) string {
	return globalProgram.trimVendor(fn)
}

var globalProgram program

type program struct {
	mu   sync.Mutex
	re   map[string]regexpCompile
	trim map[string]string
}

func (p *program) trimVendor(fn string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.trim == nil {
		p.trim = make(map[string]string)
	}
	saved, ok := p.trim[fn]
	if !ok {
		saved = fn
		if i := strings.LastIndex(saved, "/vendor/"); i >= 0 {
			saved = saved[i+len("/vendor/"):]
		}
		saved = strings.TrimPrefix(saved, "vendor/")
		p.trim[fn] = saved
	}
	return saved
}

type regexpCompile struct {
	re  *regexp.Regexp
	err error
}

func (p *program) compile(expr string) (*regexp.Regexp, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.re == nil {
		p.re = make(map[string]regexpCompile)
	}
	saved, ok := p.re[expr]
	if !ok {
		saved.re, saved.err = regexp.Compile(expr)
		p.re[expr] = saved
	}
	return saved.re, saved.err
}

func (p *program) mustCompile(expr string) *regexp.Regexp {
	re, err := p.compile(expr)
	if err != nil {
		panic(fmt.Errorf("mustCompile: %w", err))
	}
	return re
}

func (p *program) hasStackRe(stk []*trace.Frame, specs ...string) ([]int, error) {
	any := new(regexp.Regexp) // sentinel: zero or more stack frames

	res := make([]*regexp.Regexp, 0, len(specs))
	for _, spec := range specs {
		switch spec {
		case "**":
			if len(res) == 0 || res[len(res)-1] != any {
				// Collapse runs of ** into one
				res = append(res, any)
			}
		default:
			re, err := p.compile(spec)
			if err != nil {
				return nil, fmt.Errorf("could not compile regexp %q: %w", spec, err)
			}
			res = append(res, re)
		}
	}

	if len(stk) == 0 {
		if len(res) == 0 || (len(res) == 1 && res[0] == any) {
			// A zero-length stack matches an empty list of specs, and matches a
			// single **
			return []int{}, nil
		}
	}

	type path struct {
		parent *path
		frame  int
		length int
	}

	// Run the NFA, starting immediately before the first matcher
	prev := []*path{new(path)}
	for i := len(stk) - 1; i >= 0; i-- {
		// walk the stack starting at the root
		frame := stk[i]
		fn := p.trimVendor(frame.Fn)
		var next []*path
		add := func(state *path) {
			if len(next) == 0 || next[len(next)-1].length != state.length {
				next = append(next, state)
			}
		}
		for _, state := range prev {
			if state.length >= len(res) {
				continue
			}

			for j := state.length; j <= state.length+1 && j < len(res); j++ {
				// When we match against **, we need to try this frame again
				// with the next spec. Runs of ** have already been collapsed
				// into a single **.
				re := res[j]
				if re == any {
					add(state)
					add(&path{parent: state, frame: i, length: state.length + 1})
					continue
				}
				if re.MatchString(fn) {
					v := &path{parent: state, frame: i, length: state.length + 1}
					if j > state.length {
						v = &path{parent: v, frame: i, length: v.length + 1}
					}
					add(v)
				}
				break
			}
		}
		prev = next
	}

	// Check if the NFA reached the terminal state
	var matchSets [][]int
	for _, state := range prev {
		if state.length == len(res) {
			for node := state; node.parent != nil; node = node.parent {
				re := res[node.length-1]
				fn := stk[node.frame].Fn

				if re != any {
					matches := re.FindStringSubmatchIndex(fn)
					var newMatches []int
					for i := 2; i < len(matches); i += 2 {
						newMatches = append(newMatches, node.frame, matches[i], matches[i+1])
					}
					if len(newMatches) > 0 {
						matchSets = append(matchSets, newMatches)
					}
				}
			}

			allMatches := []int{} // a non-nil slice indicates that the stack matches
			for i := len(matchSets) - 1; i >= 0; i-- {
				allMatches = append(allMatches, matchSets[i]...)
			}
			return allMatches, nil
		}
	}
	return nil, nil
}

// FindStackSubmatchIndex searches stk for subexpressions described in specs. It
// returns a slice of offsets in groups of three. The first element in each
// group is number of leaf frames skipped before finding the subexpression. The
// next two elements are the start and end byte offsets within that frame's
// function name.
func FindStackSubmatchIndex(stk []*trace.Frame, specs ...string) []int {
	indexes, err := globalProgram.hasStackRe(stk, specs...)
	if err != nil {
		panic(err)
	}
	return indexes
}
