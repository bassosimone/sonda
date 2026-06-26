// SPDX-License-Identifier: GPL-3.0-or-later

// Package scan implements the `sonda scan` subcommand.
package scan

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/netstack"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

// Main is the main function of the `sonda scan` subcommand.
func Main(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.Env

	// Set command defaults.
	var (
		fail       = false
		metricsDir = "."
		spoolDir   = "."
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda scan", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.BoolVar(&fail, 'f', "fail", "Exit with error on first failure.")
	fset.StringVar(&metricsDir, 0, "metrics-dir", "Write daily Parquet files to `DIR` instead of `@DEFAULT_VALUE@`.")
	fset.StringVar(&spoolDir, 0, "spool-dir", "Use `DIR` instead of `@DEFAULT_VALUE@`.")
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit structured logs to stderr.
	logger := slog.New(slog.NewTextHandler(env.Stderr, nil))

	// Construct shared dependencies.
	measurer := netstack.NewSondaMeasurer(env, spoolDir)
	resolver := netstack.NewResolver(netstack.NewDNSOverUDPTransport(measurer))
	state := &sharedState{}

	// Build the runner registry.
	runners := map[string]stepRunner{
		"stun":           &stunRunner{Logger: logger, Measurer: measurer, Resolver: resolver, State: state},
		"dns-over-udp":   &dnsOverUDPRunner{Logger: logger, Measurer: measurer, Resolver: resolver, State: state},
		"dns-over-https": &dnsOverHTTPSRunner{Logger: logger, Measurer: measurer, Resolver: resolver, State: state},
		"https":          &httpsRunner{Logger: logger, Measurer: measurer, Resolver: resolver, State: state},
		"extract":        &extractRunner{Env: env, Logger: logger, SpoolDir: spoolDir},
		"load":           &loadRunner{Env: env, Logger: logger, MetricsDir: metricsDir, SpoolDir: spoolDir},
		"gc":             &gcRunner{Env: env, Logger: logger, SpoolDir: spoolDir},
	}

	// Execute each step in order.
	for _, step := range defaultSteps {
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
	Name string

	// Run selects the operation to execute (e.g., "stun",
	// "dns-over-udp", "dns-over-https", "https", "extract",
	// "load", "gc").
	Run string

	// With contains operation-specific parameters (e.g., "server",
	// "query", "host").
	With map[string]string
}

// stepRunner executes a step's operation.
type stepRunner interface {
	RunStep(ctx context.Context, with map[string]string) error
}

// defaultSteps defines the default scan workflow.
var defaultSteps = []singleStep{
	{Name: "STUN lookup", Run: "stun", With: map[string]string{
		"server": "stun.l.google.com",
	}},
	{Name: "DNS over UDP via Google", Run: "dns-over-udp", With: map[string]string{
		"server": "dns.google",
		"query":  "www.example.com",
	}},
	{Name: "DNS over HTTPS via Google", Run: "dns-over-https", With: map[string]string{
		"server": "dns.google",
		"query":  "www.example.com",
	}},
	{Name: "HTTPS GET www.example.com", Run: "https", With: map[string]string{
		"host": "www.example.com",
	}},
	{Name: "Extract metrics", Run: "extract", With: map[string]string{}},
	{Name: "Load metrics", Run: "load", With: map[string]string{}},
	{Name: "Garbage collect", Run: "gc", With: map[string]string{}},
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
