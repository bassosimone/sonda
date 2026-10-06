// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/bassosimone/sonda/internal/ptnopspool"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/sonda/internal/triggers"
	"github.com/bassosimone/vflag"
	"github.com/google/uuid"
)

// scanMain is the main function of the `sonda scan` subcommand.
func scanMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set the command defaults.
	var (
		fail         = false
		ptnopSocket  = config.PtnopSocketPath
		workflowFile = ""
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda-scan", vflag.ExitOnError)

	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.UsageStdout

	upr := vflag.NewDefaultUsagePrinter()
	fset.UsagePrinter = upr
	upr.AddDescription(
		"Run the steps listed in the `--workflow-file` YAML file, in order. " +
			"Each step sends a measurement request to the `sonda-inetd-ptnop` server " +
			"listening at `--ptnop-socket`, which stores the results in the spool. " +
			"A failed step does not stop the scan unless `--fail` is set.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.BoolVar(&fail, 0, "fail", "Exit with error on first failure.")
	fset.StringVar(&ptnopSocket, 0, "ptnop-socket",
		"Unix domain socket `PATH` of the `sonda-inetd-ptnop` server.",
		"Default: `@DEFAULT_VALUE@`.")
	fset.StringVar(&workflowFile, 0, "workflow-file", "Load steps from `FILE` (required).")

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit structured logs to stderr.
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Construct shared dependencies.
	state := &sharedState{}

	// TODO(bassosimone): probe IPv6 connectivity here using a UDP connect
	// to a well-known v6 address (e.g., net.Dial("udp6", "[2001:4860:4860::8888]:53")).
	// If it fails with syscall.ENETUNREACH, store "no_ipv6" in sharedState so
	// that runners can skip v6 addresses in their loops. This avoids ~8k rows/day
	// of ENETUNREACH noise on v4-only hosts. Consider a rfc6724.go file that
	// performs the check and returns a boolean. See also RFC 6724 §6 Rule 1.

	// Provide useful output when no flags were specified.
	if len(args) <= 0 {
		fset.PrintUsageString(fset.Stdout)
		env.Exit(0)
	}

	// Determine which steps to execute.
	if workflowFile == "" {
		logger.Error("no `--workflow-file` specified; nothing to do.")
		env.Exit(2)
	}
	wff, err := loadWorkflowFile(workflowFile)
	if err != nil {
		logger.Error("loading workflow", slog.Any("err", err))
		env.Exit(2)
	}

	// Connect to the ptnop server, using a single connection for all the steps.
	//
	// We connect after loading the workflow, so `scan` fails early when the server
	// is not running, but still prints the usage or workflow errors without it.
	ptnopClient, err := ptnoprpc.Dial(ctx, env, ptnopSocket)
	if err != nil {
		logger.Error("connecting to the ptnop server", slog.Any("err", err))
		env.Exit(1)
	}
	defer ptnopClient.Close()

	// Build the runner registry.
	runners := map[string]stepRunner{
		"stun":           &stunRunner{Client: ptnopClient, State: state},
		"dns-over-udp":   &dnsOverUDPRunner{Client: ptnopClient, State: state},
		"dns-over-https": &dnsOverHTTPSRunner{Client: ptnopClient, State: state},
		"https":          &httpsRunner{Client: ptnopClient, State: state},
	}

	// Execute each step in order.
	for _, step := range wff.Steps {
		runner, ok := runners[step.Run]
		if !ok {
			logger.Warn("unknown step", slog.String("run", step.Run))
			continue
		}
		if err := runner.RunStep(ctx, step.With); err != nil {
			logger.Warn("step failed", slog.String("name", step.Name), slog.Any("err", err))
			if fail {
				env.Exit(1)
			}
		}
	}

	// Execute triggers for the created spans.
	scanMustWriteTriggers(env, logger, state, wff.Triggers)
	return nil
}

// scanMustWriteTriggers writes triggers if necessary and exits on failure.
func scanMustWriteTriggers(
	env *testable.Environ, logger *slog.Logger, state *sharedState, triggerNames []string) {
	// 1. Determine whether we actually need to write triggers.
	if len(triggerNames) <= 0 {
		return
	}
	created := state.CreatedSpans()
	if len(created) <= 0 {
		return
	}

	// 2. Determine the work unit ID.
	workUnitID := uuid.Must(uuid.NewV7()).String()

	// 3. Write the work unit inside the `/run/sonda/scan` directory.
	workUnitPath := filepath.Join(config.RunDir, "scan", workUnitID+".jsonl")
	filep, err := env.OpenFile(workUnitPath, os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		logger.Error("env.OpenFile", slog.Any("err", err))
		env.Exit(1)
	}
	for _, entry := range created {
		data := runtimex.PanicOnError1(json.Marshal(entry))
		data = append(data, '\n')
		if _, err := filep.Write(data); err != nil {
			logger.Error("filep.Write", slog.Any("err", err))
			env.Exit(1)
		}
	}
	if err := filep.Close(); err != nil {
		logger.Error("filep.Close", slog.Any("err", err))
		env.Exit(1)
	}

	// 4. Create hard links to trigger the pipelines.
	for _, tname := range triggerNames {
		if !triggers.ValidName[tname] {
			logger.Warn(
				"trigger.ValidName",
				slog.String("err", "invalid trigger name"),
				slog.String("name", tname),
			)
			continue
		}
		tdirpath := triggers.Directory(tname)
		tfilepath := filepath.Join(tdirpath, workUnitID+".jsonl")
		if err := env.Link(workUnitPath, tfilepath); err != nil {
			logger.Error("env.Link", slog.Any("err", err))
			env.Exit(1)
		}
	}

	// 5. Unlink the `scan` file.
	if err := env.Remove(workUnitPath); err != nil {
		logger.Error("env.Remove", slog.Any("err", err))
		env.Exit(1)
	}
}

// singleStep describes a single operation in a scan workflow.
type singleStep struct {
	// Name is a human-readable label for this step.
	Name string `yaml:"name"`

	// Run selects the operation to execute (e.g., "stun",
	// "dns-over-udp", "dns-over-https", "https").
	Run string `yaml:"run"`

	// With contains operation-specific parameters (e.g., "server",
	// "query", "host").
	With map[string]string `yaml:"with"`
}

// stepRunner executes a step's operation.
type stepRunner interface {
	RunStep(ctx context.Context, with map[string]string) error
}

// sharedState holds state that steps can read and write during a scan.
type sharedState struct {
	mu    sync.Mutex
	spans []triggers.CreatedSpan
	tags  map[string]string
}

// SetTag sets a tag by key, overwriting any previous value.
func (s *sharedState) SetTag(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tags == nil {
		s.tags = make(map[string]string)
	}
	s.tags[key] = value
}

// Tags returns the current tags as a slice of "key=value" strings.
func (s *sharedState) Tags() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, 0, len(s.tags))
	for k, v := range s.tags {
		result = append(result, k+"="+v)
	}
	return result
}

// RunAndSave runs the req request with the given client and saves a [triggers.CreatedSpan]
// entry inside the [*sharedState] so that, at the end of the scan, we can write trigger
// files for the ETL processing pipeline according to the registered triggers.
func (s *sharedState) RunAndSave(ctx context.Context,
	client *ptnoprpc.Client, req *ptnoprpc.Request) (*ptnopspool.SpanDir, error) {
	spanDir, err := client.Run(ctx, req)
	if err != nil {
		return nil, err
	}

	cs := triggers.CreatedSpan{
		DataType: "ptnop",
		SpanDir:  spanDir.Path,
		SpanID:   spanDir.SpanID,
	}

	s.mu.Lock()
	s.spans = append(s.spans, cs)
	s.mu.Unlock()

	return spanDir, nil
}

// CreatedSpans returns the [trigger.CreatedSpan] that were added.
func (s *sharedState) CreatedSpans() []triggers.CreatedSpan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]triggers.CreatedSpan{}, s.spans...)
}
