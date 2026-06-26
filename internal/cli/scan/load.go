// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/bassosimone/sonda/internal/testable"
)

// loadRunner runs `sonda metrics load` as a subprocess.
type loadRunner struct {
	Env        *testable.Environ
	Logger     *slog.Logger
	MetricsDir string
	SpoolDir   string
}

// RunStep implements StepRunner.
func (r *loadRunner) RunStep(ctx context.Context, with map[string]string) error {
	exe, err := r.Env.Executable()
	if err != nil {
		return fmt.Errorf("load: finding executable: %w", err)
	}

	args := []string{"metrics", "load", "--spool-dir", r.SpoolDir, "--metrics-dir", r.MetricsDir}
	cmd := exec.CommandContext(ctx, exe, args...)
	if err := r.Env.RunCommand(cmd); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	return nil
}
