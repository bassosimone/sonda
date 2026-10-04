// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

// workflowFile is the top-level structure of a scan workflow file.
type workflowFile struct {
	Steps []singleStep `yaml:"steps"`
}

// loadWorkflowFile reads and parses a scan workflow file.
func loadWorkflowFile(path string) ([]singleStep, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading workflow: %w", err)
	}
	var wf workflowFile
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parsing workflow: %w", err)
	}
	if len(wf.Steps) <= 0 {
		return nil, fmt.Errorf("workflow file contains no steps")
	}
	return wf.Steps, nil
}
