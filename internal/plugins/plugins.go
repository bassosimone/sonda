// SPDX-License-Identifier: GPL-3.0-or-later

// Package plugins allows loading and using sonda plugins.
//
// A plugin is a non-setuid non-setgid executable by
// user-group-other whose path is:
//
//	$SONDA_EXEC_PATH/$name
//
// Where `$name` is a string matching `^sonda-[a-z]+$`.
//
// If `SONDA_EXEC_PATH` is empty we do not search for plugins.
//
// Plugins execute as `sonda` subcommands:
//
//	sonda $name ...
//
// causes sonda to invoke `$SONDA_EXEC_PATH/$name` reusing the
// same machinery used by the [reexec] package.
//
// Optionally, there is also a plugin description at:
//
//	$SONDA_SHARE_PATH/plugins/$name.txt
//
// If `SONDA_SHARE_PATH` is empty or there is no description file,
// `sonda --help` does not show the plugin.
//
// It is not possible to register a plugin using the name of a
// built-in command. If such a plugin exists, the code will ignore
// it and print a warning on the standard error.
//
// If `SONDA_TRACE_PLUGIN` is `1`, the plugin loading code will
// log during the loading process to help debugging.
package plugins

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bassosimone/sonda/internal/reexec"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/vclip"
)

const pluginNamePattern = `^sonda-[a-z]+$`

var pluginNameRegexp = regexp.MustCompile(pluginNamePattern)

// Load loads available plugins as `disp` subcommands.
func Load(env *testable.Environ, disp *vclip.DispatcherCommand) error {
	// 0. honor the `SONDA_TRACE_PLUGIN` var.
	var traceStderr = env.Stderr
	if env.Getenv("SONDA_TRACE_PLUGIN") != "1" {
		traceStderr = io.Discard
	}

	// 1. skip if `SONDA_EXEC_PATH` is empty/not set.
	libexecPath := env.Getenv("SONDA_EXEC_PATH")
	if libexecPath == "" {
		return nil
	}

	// 2. canonicalize the libexecPath so that the path is absolute
	//    and thus robust to subsequent chdir(2).
	libexecPath, err := env.Abs(libexecPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(traceStderr, "sonda: SONDA_EXEC_PATH=%s\n", libexecPath)

	// 3. if `SONDA_SHARE_PATH` exists, canonicalize it.
	var sharePath string
	if value := env.Getenv("SONDA_SHARE_PATH"); value != "" {
		sharePath, err = env.Abs(value)
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(traceStderr, "sonda: SONDA_SHARE_PATH=%s\n", sharePath)

	// 4. read all the libexecPath directory entries.
	entries, err := env.ReadDir(libexecPath)
	if err != nil {
		return err
	}

	// 5. process each available entry.
	for _, entry := range entries {
		name := entry.Name()
		fmt.Fprintf(traceStderr, "sonda: plugin candidate: %s\n", name)

		// 5.1. silently skip non matching entries.
		info, err := entry.Info()
		if err != nil {
			fmt.Fprintf(traceStderr, "sonda: %s: %s\n", name, err.Error())
			continue
		}
		mode := info.Mode()
		if !mode.IsRegular() {
			fmt.Fprintf(traceStderr, "sonda: %s: not a regular file\n", name)
			continue
		}
		if perms := mode.Perm() & 0111; perms != 0111 {
			fmt.Fprintf(traceStderr, "sonda: %s: invalid perms %o\n", name, perms)
			continue
		}
		if mode&(fs.ModeSetuid|fs.ModeSetgid) != 0 {
			fmt.Fprintf(traceStderr, "sonda: %s: setuid|setgid\n", name)
			continue
		}
		if !pluginNameRegexp.MatchString(name) {
			fmt.Fprintf(traceStderr, "sonda: %s: does not match: %s\n", name, pluginNamePattern)
			continue
		}

		// 5.2. prevent re-registering a name.
		//
		// We ALWAYS print a warning when a name is already taken.
		subcommandName := strings.TrimPrefix(name, "sonda-")
		if _, found := disp.Commands[subcommandName]; found {
			fmt.Fprintf(
				env.Stderr,
				"sonda: cannot register plugin: %s: command name %q already exists\n",
				name,
				subcommandName,
			)
			continue
		}

		// 5.3. check whether a description exists
		var shortDescr []string
		if sharePath != "" {
			path := filepath.Join(sharePath, "plugins", name+".txt")
			rawDescr, err := env.ReadFile(path)
			if err == nil {
				first, _, _ := bytes.Cut(rawDescr, []byte("\n"))
				shortDescr = append(shortDescr, string(first))
			}
		}

		// 5.4. register the plugin as a subcommand
		exePath := pluginExePath(filepath.Join(libexecPath, name))
		disp.AddCommand(subcommandName, exePath, shortDescr...)
		fmt.Fprintf(traceStderr, "sonda: %s: subcommandName: %s\n", name, subcommandName)
		fmt.Fprintf(traceStderr, "sonda: %s: exePath: %s\n", name, exePath)
		fmt.Fprintf(traceStderr, "sonda: %s: shortDescr: %s\n", name, shortDescr)
	}

	return nil
}

// pluginExePath is the path to the plugin executable file.
type pluginExePath string

// Main implements [vclip.Command].
func (p pluginExePath) Main(ctx context.Context, args []string) error {
	// Inject dependencies using testable.
	env := testable.ContextEnviron(ctx)

	// Override the executable to be the plugin exe path.
	env = env.Clone()
	env.Executable = func() (string, error) {
		return string(p), nil
	}
	ctx = testable.WithEnviron(ctx, env)

	// Execute using the `reexec` architecture.
	env.Exit(reexec.AsExitCode(reexec.Subcommand(ctx, args)))
	return nil
}
