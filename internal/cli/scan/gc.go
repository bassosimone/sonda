// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/bassosimone/sonda/internal/testable"
)

// gcRunner runs `sonda spool gc` as a subprocess.
type gcRunner struct {
	Env      *testable.Environ
	Logger   *slog.Logger
	SpoolDir string
}

// RunStep implements StepRunner.
func (r *gcRunner) RunStep(ctx context.Context, with map[string]string) error {
	maxAge := with["max_age"]
	if maxAge == "" {
		maxAge = "6h"
	}

	exe, err := r.Env.Executable()
	if err != nil {
		return fmt.Errorf("gc: finding executable: %w", err)
	}

	args := []string{"spool", "gc", "--spool-dir", r.SpoolDir, "--max-age", maxAge}
	cmd := exec.CommandContext(ctx, exe, args...)
	if err := r.Env.RunCommand(cmd); err != nil {
		return fmt.Errorf("gc: %w", err)
	}
	return nil
}
