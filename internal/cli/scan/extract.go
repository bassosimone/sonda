// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/bassosimone/sonda/internal/testable"
)

// extractRunner runs `sonda spool extract` as a subprocess.
type extractRunner struct {
	Env      *testable.Environ
	Logger   *slog.Logger
	SpoolDir string
}

// RunStep implements StepRunner.
func (r *extractRunner) RunStep(ctx context.Context, with map[string]string) error {
	maxAge := with["max_age"]
	if maxAge == "" {
		maxAge = "1h"
	}

	exe, err := r.Env.Executable()
	if err != nil {
		return fmt.Errorf("extract: finding executable: %w", err)
	}

	args := []string{"spool", "extract", "--spool-dir", r.SpoolDir, "--max-age", maxAge}
	cmd := exec.CommandContext(ctx, exe, args...)
	if err := r.Env.RunCommand(cmd); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	return nil
}
