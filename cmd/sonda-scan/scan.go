// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/config"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

// scanMain is the main function of the `sonda scan` subcommand.
func scanMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Set command defaults.
	var (
		fail         = false
		metricsDir   = "."
		ptnopSocket  = config.PtnopSocketPath
		spoolDir     = "."
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
			"Measurement steps send requests to the `sonda-inetd-ptnop` server " +
			"listening at `--ptnop-socket`, which must store its results under " +
			"`<spool-dir>/ptnop`. " +
			"The `extract` and `load` steps process that spool and write " +
			"daily metrics under `<metrics-dir>/qoe`. A failed step does not stop " +
			"the scan unless `--fail` is set.")

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.BoolVar(&fail, 0, "fail", "Exit with error on first failure.")
	fset.StringVar(&metricsDir, 0, "metrics-dir",
		"Top-level `DIR` containing processed metrics.",
		"Default: `@DEFAULT_VALUE@`.")
	fset.StringVar(&ptnopSocket, 0, "ptnop-socket",
		"Unix domain socket `PATH` of the `sonda-inetd-ptnop` server.",
		"Default: `@DEFAULT_VALUE@`.")
	fset.StringVar(&spoolDir, 0, "spool-dir",
		"Top-level `DIR` containing raw measurement results.",
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
	steps, err := loadWorkflowFile(workflowFile)
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
		"extract":        &extractRunner{Env: env, Logger: logger, SpoolDir: spoolDir},
		"load":           &loadRunner{Env: env, Logger: logger, MetricsDir: metricsDir, SpoolDir: spoolDir},
	}

	// Execute each step in order.
	for _, step := range steps {
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

	return nil
}

// singleStep describes a single operation in a scan workflow.
type singleStep struct {
	// Name is a human-readable label for this step.
	Name string `yaml:"name"`

	// Run selects the operation to execute (e.g., "stun",
	// "dns-over-udp", "dns-over-https", "https", "extract",
	// "load").
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
	mu   sync.Mutex
	tags map[string]string
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
