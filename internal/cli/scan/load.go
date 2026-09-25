// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"context"
	"fmt"
	"log/slog"

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
	args := []string{"metrics", "load", "--spool-dir", r.SpoolDir, "--metrics-dir", r.MetricsDir}
	if err := r.Env.ReExec(ctx, args); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	return nil
}
