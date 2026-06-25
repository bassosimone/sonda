// SPDX-License-Identifier: GPL-3.0-or-later

package netstack

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/bassosimone/sonda/internal/paths"
)

// STUNLookupper looks up the public IP address using [SondaMeasurer].
//
// Use [NewSTUNLookupper] to construct.
type STUNLookupper struct {
	// Measurer is the measurement runner.
	//
	// Set by [NewSTUNLookupper].
	Measurer *SondaMeasurer

	// ServerAddr is the STUN server address and port.
	//
	// Default: "74.125.250.129:19302".
	ServerAddr string

	// Timeout is the timeout for the STUN request.
	//
	// Default: 5s.
	Timeout time.Duration
}

// NewSTUNLookupper creates a [*STUNLookupper] with sensible defaults.
func NewSTUNLookupper(measurer *SondaMeasurer) *STUNLookupper {
	return &STUNLookupper{
		Measurer:   measurer,
		ServerAddr: "74.125.250.129:19302",
		Timeout:    5 * time.Second,
	}
}

// LookupIPAddr returns the reflexive IP address as seen by the STUN server.
func (lk *STUNLookupper) LookupIPAddr(ctx context.Context) (string, error) {
	spanDir, err := lk.Measurer.Run(ctx, &SondaMeasureSTUN{
		Target:  lk.ServerAddr,
		Timeout: lk.Timeout,
	})
	if err != nil {
		return "", err
	}
	return readReflexiveAddr(spanDir)
}

// readReflexiveAddr reads stdout.txt from a span directory and extracts
// the reflexive IP address from the "reflexiveAddress" message.
func readReflexiveAddr(spanDir string) (string, error) {
	filep, err := os.Open(paths.SpanStdout(spanDir))
	if err != nil {
		return "", err
	}
	defer filep.Close()

	scanner := bufio.NewScanner(filep)
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry["msg"] != "reflexiveAddress" {
			continue
		}
		ip, ok := entry["ip"].(string)
		if !ok {
			continue
		}
		return ip, nil
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no reflexive address found")
}
