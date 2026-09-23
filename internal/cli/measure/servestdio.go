// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/bassosimone/deferexit"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vflag"
)

/*
	Security considerations
	-----------------------

	Threat model:

	1. the peer writes requests on our stdin and therefore controls
	   the argv and the environment variables of each served command.
	   We trust it because the transport implies that the peer is the
	   parent process, running as the same user as this process, so
	   it could already do by itself anything it asks us to do.

	2. this assumption stops holding for any listening transport
	   (Unix socket, TCP). Reusing this code there requires at least
	   peer authentication, a server-owned body directory, and a
	   policy on which targets may be measured.

	What the peer controls through a request:

	3. file paths, through options such as `http --body-file`, which
	   would otherwise allow writing attacker-chosen content (the
	   response body) at arbitrary locations.

	4. network targets, through `--target`, which makes this process
	   a proxy into whatever network it can reach.

	5. environment variables, which matter only for child processes
	   (PATH, LD_LIBRARY_PATH): they never modify the environment of
	   this process, and served commands do not exec children.

	Mitigations implemented in this file:

	6. the request environment is reduced to SONDA_SPAN_ID; every
	   other variable is dropped before serving.

	7. file operations go through [os.OpenRoot] on the `-C` directory,
	   so paths must be relative to it and cannot escape it.

	8. the dialer only allows TCP and UDP, so a served command cannot
	   create Unix domain sockets at arbitrary paths.

	9. RunCommand always fails and stdin is replaced with an empty
	   reader, so served commands cannot exec or consume requests.

	What makes the mitigations effective:

	10. served commands reach the OS and the network only through the
	    [*testable.Environ] bound to the request context. Any direct
	    use of `os`, `net`, or `os/exec` in a served command bypasses
	    every mitigation above. Review each change to this package for
	    such calls (grep for `os.`, `net.`, `exec.` outside this file)
	    and route them through the environment. The `nop` library
	    honors this because it dials only through `Config.Dialer`;
	    its HTTP transport wraps the already-dialed connection. The
	    TLS stack reads the system root store, which is read-only.

	Defense in depth:

	11. the environment discipline above is a code-level layer and a
	    single missed call defeats it. Deployments should add an OS
	    layer that does not depend on our code being right: under
	    systemd, sandboxing directives such as ProtectSystem,
	    ProtectHome, PrivateTmp, and RestrictAddressFamilies (see
	    systemd.exec(5)); as user code, a bwrap(1) sandbox that mounts
	    only the `-C` directory read-write. Each layer has holes; the
	    point is that they do not line up.
*/

// serveStdioRequest is a request sent over the stdin
// to the `sonda measure serve stdio` subcommand.
type serveStdioRequest struct {
	// Args contains the command line arguments to pass
	// to the `sonda measure` process.
	Args []string `json:"args"`

	// Envs contains the environment variables to set
	// inside the `sonda measure` execution.
	Envs map[string]string `json:"envs"`
}

// serveStdioGoroutineInput contains the input for one of
// the goroutines that serve input requests.
type serveStdioGoroutineInput struct {
	env *testable.Environ
	req *serveStdioRequest
}

// serveStdioMain is the main function of the `sonda measure serve stdio` subcommand.
func serveStdioMain(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Avoid re-entering into the service.
	if env.Getenv("SONDA_MEASURE_SERVE") == "1" {
		fmt.Fprintf(env.Stderr, "re-entrant `serve stdio` invocation.\n")
		env.Exit(1)
	}

	// Set command defaults.
	var (
		restrictDir = "."
	)

	// Parse command line flags.
	fset := vflag.NewFlagSet("sonda measure serve stdio", vflag.ExitOnError)
	fset.Exit = env.Exit
	fset.Stderr = env.Stderr
	fset.Stdout = env.Stdout

	fset.AutoHelp('h', "help", "Show this help message and exit.")
	fset.StringVar(
		&restrictDir, 'C', "directory",
		"Restrict operations to the `DIR` directory and subdirectories.",
		"Default: the current working directory.",
	)

	runtimex.PanicOnError0(fset.Parse(args)) // cannot fail: using vflag.ExitOnError

	// Make the environment robust with respect to
	// concurrent writes to the stdout/stderr.
	env = env.Clone()
	env.Stdout = &serveStdioWriterLocked{out: env.Stdout}
	env.Stderr = &serveStdioWriterLocked{out: env.Stderr}

	// Prevent children from reading stdin because the stdin
	// is owned by this goroutine until exit.
	stdin := env.Stdin
	env.Stdin = bytes.NewReader(nil)

	// Open the specified directory and restrict filesystem
	// operations inside such a directory.
	fsroot, err := os.OpenRoot(restrictDir)
	if err != nil {
		fmt.Fprintf(env.Stderr, "%s\n", err.Error())
		env.Exit(1)
		return err // just in case env.Exit does not exit
	}
	env.MkdirAll = fsroot.MkdirAll
	env.OpenFile = func(name string, flag int, perm os.FileMode) (testable.File, error) {
		return fsroot.OpenFile(name, flag, perm)
	}
	env.Rename = fsroot.Rename
	env.WriteFile = fsroot.WriteFile

	// Prevent executing external commands as well as rexecution.
	env.RunCommand = func(cmd *exec.Cmd) error {
		return fs.ErrNotExist
	}
	env.ReExec = func(ctx context.Context, args []string) error {
		return testable.ErrNoReExec
	}

	// Restrict the kind of sockets we can create.
	env.Dialer = &serveStdioDialer{env.Dialer}

	// Ensure we wait for pending requests to finish. Keep in mind
	// that `deferexit` turns exit calls into panics so we end up here
	// if the main loop invokes `env.Exit`.
	wg := &sync.WaitGroup{}
	defer func() {
		fmt.Fprintf(env.Stderr, "waiting for requests to complete...\n")
		wg.Wait()
	}()

	// Create concurrent goroutines for serving requests
	const (
		parallelism = 8
		maxqueue    = 128
	)
	inputch := make(chan *serveStdioGoroutineInput, maxqueue)
	defer close(inputch)
	for range parallelism {
		wg.Go(func() {
			for x := range inputch {
				serveStdioServe(ctx, x.env, x.req, false /* not overloaded */)
			}
		})
	}

	// Read and process each argv from the stdin.
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		// Get the line from the stdin
		line := scanner.Bytes()

		// Parse the request
		var req serveStdioRequest
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(env.Stderr, "%s\n", err.Error())
			env.Exit(1)
			return err // just in case env.Exit does not exit
		}

		// Make sure the request sets the SONDA_SPAN_ID environment variable
		// so that the client can distinguish the responses
		if value, ok := req.Envs["SONDA_SPAN_ID"]; !ok || value == "" {
			err := errors.New("missing SONDA_SPAN_ID environment variable")
			fmt.Fprintf(env.Stderr, "%s\n", err.Error())
			env.Exit(1)
			return err // just in case env.Exit does not exit
		}

		// Post the work for a worker goroutine dropping
		// the work if we are currently overloaded.
		x := &serveStdioGoroutineInput{
			env: env,
			req: &req,
		}
		select {
		case inputch <- x:
		default:
			serveStdioServe(ctx, x.env, x.req, true /* overloaded */)
		}
	}

	// Handle the case of I/O error.
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(env.Stderr, "%s\n", err.Error())
		env.Exit(1)
		return err // just in case env.Exit does not exit
	}
	return nil
}

// serveStdioServe serves a single request.
func serveStdioServe(
	ctx context.Context,
	env *testable.Environ,
	req *serveStdioRequest,
	overload bool,
) {
	// We assume that the caller has checked for this.
	spanID := req.Envs["SONDA_SPAN_ID"]
	runtimex.Assert(spanID != "")

	// Setup a modified environment for this request that
	// communicates we're running inside a service and that
	// honors the variables set by the caller.
	//
	// SECURITY: see considerations at the top of this file
	overrides := []string{"SONDA_MEASURE_SERVE=1"}
	for key, value := range req.Envs {
		switch key {
		case "SONDA_SPAN_ID":
			overrides = append(overrides, key+"="+value)
		default:
			// Ignore the variable
		}
	}
	env = testable.WithEnvOverrides(env, overrides...)

	// Ensure we trap any env.Exit invocation by the callee
	// and route it to become an exitcode related event
	defer deferexit.Recover(func(exitcode int) {
		logger := slog.New(slog.NewJSONHandler(env.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
		logger = logger.With("spanID", spanID)
		logger.Info("sondaExit", slog.Int("exitcode", exitcode))
	})

	// Bind the context to the new environment
	ctx = testable.WithEnviron(ctx, env)

	// Handle the case where we're actually overloaded
	if overload {
		env.Exit(75) // EX_TEMPFAIL - "temporary failure; the user is invited to retry"
		return
	}

	// Defer to the module main function and make sure we
	// invoke Exit(0) if we ever return from Main
	Main(ctx, req.Args)
	env.Exit(0)
}

// serveStdioWriterLocked is a mutex-locked [io.Writer] wrapper.
type serveStdioWriterLocked struct {
	mu  sync.Mutex
	out io.Writer
}

var _ io.Writer = &serveStdioWriterLocked{}

// Write implements [io.Writer].
func (s *serveStdioWriterLocked) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.Write(data)
}

type serveStdioDialer struct {
	dialer testable.Dialer
}

var _ testable.Dialer = &serveStdioDialer{}

// DialContext implements [testable.Dialer].
func (s *serveStdioDialer) DialContext(
	ctx context.Context, network string, address string) (net.Conn, error) {
	switch network {
	case "udp", "udp4", "udp6", "tcp", "tcp4", "tcp6":
		return s.dialer.DialContext(ctx, network, address)
	default:
		return nil, syscall.EINVAL
	}
}
