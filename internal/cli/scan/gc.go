// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"fmt"
	"log/slog"

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

	args := []string{"spool", "gc", "--spool-dir", r.SpoolDir, "--max-age", maxAge}
	if err := r.Env.ReExec(ctx, args); err != nil {
		return fmt.Errorf("gc: %w", err)
	}
	return nil
}
