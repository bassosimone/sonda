// SPDX-License-Identifier: GPL-3.0-or-later

package scan

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

// configFile is the top-level structure of a scan config file.
type configFile struct {
	Steps []singleStep `yaml:"steps"`
}

// loadConfigFile reads and parses a scan config file.
func loadConfigFile(path string) ([]singleStep, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if len(cfg.Steps) <= 0 {
		return nil, fmt.Errorf("config file contains no steps")
	}
	return cfg.Steps, nil
}
