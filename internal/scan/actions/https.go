// SPDX-License-Identifier: GPL-3.0-or-later

package actions

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/ptnoprpc"
	"github.com/goccy/go-yaml"
)

// httpsAction is the action that measures HTTPS endpoints.
type httpsAction struct {
	Conn   WorkflowPtnopConn
	Runner *WorkflowRunner
}

// newHTTPSAction constructs a new [*httpsAction] instance.
func newHTTPSAction(conn WorkflowPtnopConn, runner *WorkflowRunner) workflowAction {
	return &httpsAction{
		Conn:   conn,
		Runner: runner,
	}
}

// HTTPSWith contains the "with" block parsed by https.
type HTTPSWith struct {
	Host      string `yaml:"host"`
	Optimize  string `yaml:"optimize"`
	Port      uint16 `yaml:"port"`
	URLPath   string `yaml:"url_path"`
	UserAgent string `yaml:"user_agent"`
}

var _ workflowAction = &httpsAction{}

// Run implements [workflowAction].
func (a *httpsAction) Run(
	ctx context.Context, logger *slog.Logger, step *WorkflowStep) error {
	// 1. Parse arguments passed to the step.
	with := step.With
	runtimex.Assert(with != nil)
	var withData HTTPSWith
	if err := yaml.NodeToValue(with, &withData); err != nil {
		return err
	}
	if withData.Host == "" {
		return fmt.Errorf("https: missing 'host' parameter")
	}
	if withData.Port == 0 {
		withData.Port = 443
	}
	if withData.URLPath == "" {
		withData.URLPath = "/"
	}
	var headers []string
	if value := withData.UserAgent; value != "" {
		headers = append(headers, "User-Agent: "+value)
	}

	// 2. Resolve the host to addresses.
	addrs, err := a.Runner.lookupHost(ctx, a.Conn, withData.Host, logger, withData.Optimize)
	if err != nil {
		return fmt.Errorf("https: resolving %s: %w", withData.Host, err)
	}

	// 3. Perform an HTTPS GET against each resolved address.
	for _, addr := range addrs {
		req := &ptnoprpc.Request{
			ALPN:         []string{"h2", "http/1.1"},
			AddrPort:     net.JoinHostPort(addr, strconv.Itoa(int(withData.Port))), // uint16 -> int
			HTTPBodyFile: false,
			HTTPHeaders:  headers,
			HTTPHost:     withData.Host,
			HTTPMethod:   "GET",
			HTTPScheme:   "https",
			Optimize:     withData.Optimize,
			Pipeline:     "https",
			SNI:          withData.Host,
			Tags:         a.Runner.tags(),
			Timeout:      30 * time.Second,
			URLPath:      withData.URLPath,
		}
		if _, err := a.Runner.runPtnop(ctx, a.Conn, logger, req); err != nil {
			return fmt.Errorf("https: ptnop: %w", err)
		}
	}
	return nil
}
