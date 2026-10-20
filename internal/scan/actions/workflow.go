// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/bassosimone/sonda/internal/ptnopclient"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/bassosimone/sonda/internal/spool"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/sonda/internal/triggers"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// WorkflowSpec is the complete specification of a measurement workflow.
type WorkflowSpec struct {
	// Cadence indicates the step cadence.
	//
	// Valid values:
	//
	// - "@frequent" runs on the average every five minutes.
	// - "@hourly" runs on the average every hour.
	// - "@sporadic" runs on the average every six hours.
	Cadence string `yaml:"cadence"`

	// Steps contains the workflow steps.
	Steps []WorkflowStep `yaml:"steps"`

	// Triggers contains the names of the ETLs to trigger
	// once the workflow results are ready.
	//
	// See [triggers.ValidName].
	Triggers []string `yaml:"triggers"`
}

// LoadWorkflowSpec loads the [*WorkflowSpec] at the given path.
func LoadWorkflowSpec(env *testable.Environ, path string) (*WorkflowSpec, error) {
	data, err := env.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading workflow: %w", err)
	}

	var wf WorkflowSpec
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parsing workflow: %w", err)
	}
	if len(wf.Steps) <= 0 {
		return nil, fmt.Errorf("no steps")
	}

	for _, t := range wf.Triggers {
		if ok := triggers.ValidName[t]; !ok {
			return nil, fmt.Errorf("invalid trigger name: %s", t)
		}
	}

	return &wf, nil
}

// WorkflowStep is a measurement spec within a [*WorkflowSpec].
type WorkflowStep struct {
	// Name is the human-readable step label.
	Name string `yaml:"name"`

	// Run selects the measurement action to execute (e.g., "dns-over-https").
	Run string `yaml:"run"`

	// With contains action-specific parameters.
	With ast.Node `yaml:"with"`
}

// WorkflowAction abstracts over measurement actions.
type WorkflowAction interface {
	Run(ctx context.Context, logger *slog.Logger, with ast.Node) error
}

// unknownAction is the action returned if the action name is unknown.
type unknownAction struct {
	name string
}

var _ WorkflowAction = unknownAction{}

// Run implements [WorkflowAction].
func (a unknownAction) Run(ctx context.Context, logger *slog.Logger, with ast.Node) error {
	logger.Warn("unknownAction", slog.String("name", a.name))
	return nil
}

// actionConstructor is the type of the func that constructs a new [WorkflowAction].
type actionConstructor func(WorkflowPtnopConn, *WorkflowRunner) WorkflowAction

// WorkflowRunner runs a workflow and keeps shared state.
//
// Use [NewWorkflowRunner] to construct.
type WorkflowRunner struct {
	actions      map[string]actionConstructor
	mu           sync.Mutex
	spans        []triggers.CreatedSpan
	spoolRootDir spool.RootDir
	tagm         map[string]string
	v6sup        *bool
}

// NewWorkflowRunner constructs a [*WorkflowRunner] instance.
func NewWorkflowRunner(rootDir spool.RootDir) *WorkflowRunner {
	return &WorkflowRunner{
		actions: map[string]actionConstructor{
			"https":          newHTTPSAction,
			"dns-over-https": newDNSOverHTTPSAction,
			"dns-over-udp":   newDNSOverUDPAction,
			"stun":           newSTUNAction,
		},
		mu:           sync.Mutex{},
		spans:        []triggers.CreatedSpan{},
		spoolRootDir: rootDir,
		tagm:         map[string]string{},
		v6sup:        nil, // this is a tri-state and nil means: we don't know yet
	}
}

// NewAction constructs a new [WorkflowAction] by name.
func (r *WorkflowRunner) NewAction(conn WorkflowPtnopConn, name string) WorkflowAction {
	ctor := r.actions[name]
	if ctor == nil {
		return unknownAction{name} // no need to wrap since it's a no-op
	}
	return newActionWrapper(ctor(conn, r), name)
}

// actionWrapper wraps an action execution.
type actionWrapper struct {
	child WorkflowAction
	name  string
}

// newActionWrapper creates a new [*actionWrapper].
func newActionWrapper(child WorkflowAction, name string) *actionWrapper {
	return &actionWrapper{
		child: child,
		name:  name,
	}
}

var _ WorkflowAction = &actionWrapper{}

// Run implements [WorkflowAction].
func (a *actionWrapper) Run(ctx context.Context, logger *slog.Logger, with ast.Node) error {
	if with == nil {
		logger.Warn("missingActionWith", slog.String("name", a.name))
		return nil
	}
	logger.Info("actionStarted", slog.String("name", a.name))
	err := a.child.Run(ctx, logger, with)
	logger.Info("actionDone", slog.Any("err", err), slog.String("name", a.name))
	return err
}

// setSupportsV6 sets the value of the "supportsV6" internal variable.
func (r *WorkflowRunner) setSupportsV6(value bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.v6sup = &value
}

// supportsV6 returns whether we support IPv6 or nil if unknown.
func (r *WorkflowRunner) supportsV6() *bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.v6sup == nil {
		return nil
	}
	value := *r.v6sup
	return &value
}

// setTag sets a tag by key, overwriting any previous value.
func (r *WorkflowRunner) setTag(key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tagm[key] = value
}

// tags returns the current tags as a slice of "key=value" strings.
func (r *WorkflowRunner) tags() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, 0, len(r.tagm))
	for k, v := range r.tagm {
		result = append(result, k+"="+v)
	}
	return result
}

// WorkflowPtnopConn abstracts over a [*ptnopclient.Conn].
type WorkflowPtnopConn interface {
	Run(ctx context.Context, req *ptnoprpc.Request) (*ptnopclient.RunResult, error)
}

// runPtnop runs a submeasurement using ptnop and returns the related [spool.SpanDir].
func (r *WorkflowRunner) runPtnop(
	ctx context.Context, conn WorkflowPtnopConn, req *ptnoprpc.Request) (spool.SpanDir, error) {
	result, err := conn.Run(ctx, req)
	if err != nil {
		return nil, err
	}

	spanDir, err := spool.OpenSpanDir(r.spoolRootDir, "ptnop", result.SpanID)
	if err != nil {
		return nil, err
	}

	cs := triggers.CreatedSpan{
		DataType: spanDir.DataType(),
		SpanID:   spanDir.ID(),
	}
	r.mu.Lock()
	r.spans = append(r.spans, cs)
	r.mu.Unlock()

	return spanDir, nil
}

// CreatedSpans returns the [triggers.CreatedSpan] added as part of the run.
func (r *WorkflowRunner) CreatedSpans() []triggers.CreatedSpan {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]triggers.CreatedSpan{}, r.spans...)
}
