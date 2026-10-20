// SPDX-License-Identifier: GPL-3.0-or-later

// Package scheduler contains code to schedule workflows.
package scheduler

import (
	"encoding/json"
	"math/rand/v2"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/scan/actions"
	"github.com/bassosimone/sonda/internal/testable"
)

// State is the state serialized on disk.
//
// Use [LoadState] to construct.
type State struct {
	Workflows map[string]*WorkflowState `json:"workflows"`
	path      string                    `json:"-"` // private and explicitly not exported
}

// newState creates and returns an empty [*State].
func newState(path string) *State {
	return &State{
		Workflows: map[string]*WorkflowState{},
		path:      path,
	}
}

// WorkflowState contains workflow specific state.
type WorkflowState struct {
	LastRun time.Time `json:"lastRun"`
	NextRun time.Time `json:"nextRun"`
}

// LoadState loads the serialized state from the given path. On error, the
// return value contains a valid [*State] in all cases except the one in which
// we cannot make the given path absolute. This behavior allows you to deal
// with a missing or corrupt state file and recover.
//
// We unconditionally prune [*State] entries whose LastRun is older than a week.
func LoadState(env *testable.Environ, path string) (*State, error) {
	path, err := env.Abs(path) // robustness against chdir
	if err != nil {
		return nil, err
	}

	data, err := env.ReadFile(path)
	if err != nil {
		return newState(path), err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return newState(path), err
	}
	state.path = path // remember where to serialize

	if state.Workflows == nil {
		state.Workflows = make(map[string]*WorkflowState)
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour) // one week ago
	veryOld := []string{}
	for path, entry := range state.Workflows {
		if cutoff.After(entry.LastRun) {
			veryOld = append(veryOld, path)
		}
	}
	for _, path := range veryOld {
		delete(state.Workflows, path)
	}

	return &state, nil
}

// Path returns the absolute path where the state is stored.
func (s *State) Path() string {
	return s.path
}

// Store stores the serialized state to disk using a temporary file
// to guarantee that no partial content can be read.
func (s *State) Store(env *testable.Environ) error {
	data := runtimex.PanicOnError1(json.Marshal(s))
	tmpPath := s.path + ".tmp"
	if err := env.WriteFile(tmpPath, data, 0640); err != nil {
		return err
	}
	return env.Rename(tmpPath, s.path)
}

// WorkflowSpec wraps [*actions.WorkflowSpec] to add the absolute
// file path from which the spec has been loaded.
//
// We need to record the absolute path because that is the key
// used to distinguish between workflow files. Thus, renaming a
// specific workflow resets its schedule.
//
// Construct using [LoadWorkflowSpec].
type WorkflowSpec struct {
	Data *actions.WorkflowSpec
	Path string
}

// LoadWorkflowSpec loads a [*WorkflowSpec] from the given path.
func (s *State) LoadWorkflowSpec(env *testable.Environ, path string) (*WorkflowSpec, error) {
	path, err := env.Abs(path) // absolute path is needed
	if err != nil {
		return nil, err
	}

	spec, err := actions.LoadWorkflowSpec(env, path)
	if err != nil {
		return nil, err
	}

	wrapper := &WorkflowSpec{
		Data: spec,
		Path: path,
	}
	return wrapper, nil
}

// ShouldRun returns whether the given [*WorkflowSpec] should run now
// based on its NextRun value and the current time.
func (s *State) ShouldRun(spec *WorkflowSpec) bool {
	state, found := s.Workflows[spec.Path]
	if !found {
		state = &WorkflowState{}
		s.Workflows[spec.Path] = state
	}
	return state.NextRun.Before(time.Now())
}

// DidRun registers that the given [*WorkflowSpec] did actually run
// and computes when the next run should occur.
//
// The algorithm for doing so is brutally simple:
//
//  1. determine the expected delay value based on the Cadence treating
//     any unknown Cadence value as "@daily".
//
//  2. extract an exponential distribution sample with expected
//     value equal to the expected value and clamp it so that
//     the lower bound is 10% of the expected value and the upper
//     bound is 250% of the expected value (thus restricting the
//     sampling between ~10p and ~90p).
//
//  3. record the NextRun as the current time plus the clamped delay.
//
// Regardless of whether a run failed or not, we expect the caller
// to always register it using this method.
func (s *State) DidRun(spec *WorkflowSpec) {
	state, found := s.Workflows[spec.Path]
	if !found {
		state = &WorkflowState{}
		s.Workflows[spec.Path] = state
	}

	state.LastRun = time.Now()

	// (1)
	var expected float64
	switch spec.Data.Cadence {
	case "@frequent":
		expected = float64(5 * time.Minute)
	case "@hourly":
		expected = float64(time.Hour)
	case "@daily/4":
		expected = float64(6 * time.Hour)
	default:
		expected = float64(24 * time.Hour)
	}

	// (2)
	minSample, maxSample := expected/10, expected*2.5
	sample := rand.ExpFloat64() * expected
	sample = max(minSample, min(maxSample, sample))

	// (3)
	state.NextRun = state.LastRun.Add(time.Duration(sample))
}
