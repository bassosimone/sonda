// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"log/slog"
	"net/netip"
	"time"

	"github.com/bassosimone/errclass"
	"github.com/bassosimone/nop"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
	"github.com/pion/stun/v3"
)

// stunMain is the main function of the `sonda measure stun` subcommand.
func stunMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.Env

	// Set command defaults.
	var (
		target  = "74.125.250.129:19302"
		spanID  = nop.NewSpanID()
		timeout = 30 * time.Second
	)

	// Honor SONDA_SPAN_ID environment variable.
	if v := env.Getenv("SONDA_SPAN_ID"); v != "" {
		spanID = v
	}

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda measure stun", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout
	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(&spanID, 0, "span-id", "Use `ID` instead of a random one. Honors `SONDA_SPAN_ID`.")
	fset.StringVar(&target, 0, "target", "Use `ADDR:PORT` instead of `@DEFAULT_VALUE@`.")
	fset.DurationVar(&timeout, 0, "timeout", "Use `DURATION` instead of `@DEFAULT_VALUE@`.")
	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Emit structured logs to the stdout tied together by a span ID.
	logger := slog.New(slog.NewJSONHandler(env.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	logger = logger.With("spanID", spanID)

	// Log the measurement start / done events.
	logger.Info("measurementStart")
	defer logger.Info("measurementDone")

	// Log the command line arguments for reproducibility.
	fullArgs := append([]string{"sonda", "measure", "stun"}, args...)
	logger.Info("commandLineArgs", slog.Any("args", fullArgs))

	// Parse target as an endpoint.
	epnt, err := netip.ParseAddrPort(target)
	if err != nil {
		logger.Error("parseTargetFailed", slog.Any("err", err))
		env.Exit(2)
	}

	// Configure the pipeline timeout.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create the shared pipeline configuration.
	cfg := nop.NewConfig()
	cfg.Dialer = env.Dialer
	cfg.ErrClassifier = nop.ErrClassifierFunc(errclass.New)

	// Create the dialing pipeline (UDP connect, no protocol wrapping).
	epntOp := nop.NewEndpointFunc(epnt)
	connectOp := nop.NewConnectFunc(cfg, "udp", logger)
	observeOp := nop.NewObserveConnFunc(cfg, logger)
	autoCancelOp := nop.NewCancelWatchFunc()
	dialPipe := nop.Compose4(epntOp, connectOp, observeOp, autoCancelOp)

	// Dial the UDP connection.
	conn, err := dialPipe.Call(ctx, nop.Unit{})
	if err != nil {
		logger.Error("dialFailed", slog.Any("err", err))
		env.Exit(1)
	}
	defer conn.Close()

	// Build STUN binding request using pion/stun as codec.
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

	// Write the binding request.
	if _, err := conn.Write(message.Raw); err != nil {
		logger.Error("writeFailed", slog.Any("err", err))
		env.Exit(1)
	}

	// Read the binding response.
	buf := make([]byte, 1024)
	nread, err := conn.Read(buf)
	if err != nil {
		logger.Error("readFailed", slog.Any("err", err))
		env.Exit(1)
	}

	// Decode the response using pion/stun as codec.
	resp := new(stun.Message)
	resp.Raw = buf[:nread]
	if err := resp.Decode(); err != nil {
		logger.Error("decodeFailed", slog.Any("err", err))
		env.Exit(1)
	}

	// Extract the reflexive address.
	var xorAddr stun.XORMappedAddress
	if err := xorAddr.GetFrom(resp); err != nil {
		logger.Error("getXORMappedAddressFailed", slog.Any("err", err))
		env.Exit(1)
	}

	logger.Info("reflexiveAddress",
		slog.String("ip", xorAddr.IP.String()),
		slog.Int("port", xorAddr.Port),
	)

	return nil
}
