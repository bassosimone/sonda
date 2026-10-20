// SPDX-License-Identifier: GPL-3.0-or-later

// Package scheduler contains code to schedule workflows.
package scheduler

import (
	"encoding/json"
	"errors"
	"io/fs"
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
// return value contains a valid [*State] when the file does not exist or parsing
// the file is not possible (indicating a corrupt file). In both cases, you
// should just log a warning and continue with the returned [*State].
//
// We unconditionally prune [*State] entries whose LastRun is older than a week.
func LoadState(env *testable.Environ, path string) (*State, error) {
	path, err := env.Abs(path) // robustness against chdir
	if err != nil {
		return nil, err
	}

	data, err := env.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return newState(path), err
		}
		return nil, err
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

// GetOrCreateWorkflowState returns the [*WorkflowState] if it already
// exists and otherwise registers and returns an empty state.
func (s *State) GetOrCreateWorkflowState(spec *WorkflowSpec) *WorkflowState {
	state, found := s.Workflows[spec.Path]
	if !found {
		state = &WorkflowState{}
		s.Workflows[spec.Path] = state
	}
	return state
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

// NextRunTime computes the next run time for this [*WorkflowSpec].
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
//  3. return current time plus the clamped delay.
func (wfs *WorkflowSpec) NextRunTime() time.Time {
	// (1)
	var expected float64
	switch wfs.Data.Cadence {
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
	return time.Now().Add(time.Duration(sample))
}

// ShouldRun returns whether the given [*WorkflowSpec] should run now
// based on its NextRun value and the current time.
func (s *State) ShouldRun(spec *WorkflowSpec) bool {
	return s.GetOrCreateWorkflowState(spec).NextRun.Before(time.Now())
}

func (s *State) setTimesAndSync(env *testable.Environ, spec *WorkflowSpec) error {
	state := s.GetOrCreateWorkflowState(spec)
	state.LastRun = time.Now()
	state.NextRun = spec.NextRunTime()
	return s.Store(env)
}

// WillStart marks the [*WorkflowSpec] as about to start.
//
// To be robust against crashes causing workflows to loop we update the
// LastRun and recompute NextRun and then sync the state to disk.
//
// If this method fails, you should not start the workflow because it
// means the state has not been persisted to disk.
func (s *State) WillStart(env *testable.Environ, spec *WorkflowSpec) error {
	return s.setTimesAndSync(env, spec)
}

// DidRun marks the [*WorkflowSpec] as terminated.
//
// We update LastRun and recompute NextRun, then sync the state to disk. We do this,
// and specifically recompute NextRun, so that the delay between two subsequent
// workflow invocations is not reduced by the total workflow runtime.
func (s *State) DidRun(env *testable.Environ, spec *WorkflowSpec) error {
	return s.setTimesAndSync(env, spec)
}
