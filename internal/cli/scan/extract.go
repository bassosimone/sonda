// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"fmt"
	"log/slog"

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

	args := []string{"spool", "extract", "--spool-dir", r.SpoolDir, "--max-age", maxAge}
	if err := r.Env.ReExec(ctx, args); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	return nil
}
