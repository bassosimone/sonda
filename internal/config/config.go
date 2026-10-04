// SPDX-License-Identifier: GPL-3.0-or-later

// Package config allows reading the default config file.
package config

import (
	"encoding"
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/bassosimone/sonda/internal/testable"
)

// DefaultConfigFilePath contains the default config file path.
const DefaultConfigFilePath = "/etc/sonda/config.toml"

// PtnopSocketPath is the `sonda-inetd-ptnop` socket path.
//
// Not configurable: it must match `ListenStream=` in `sonda-inetd-ptnop.socket`.
const PtnopSocketPath = "/run/sonda/inetd-ptnop.sock"

// MetricsDir is the top-level metrics directory.
//
// Not configurable: `postinst` creates it and `sonda-scan.service` lists
// it in `ReadWritePaths=`. To relocate it, use a bind mount or a symlink.
const MetricsDir = "/var/lib/sonda/metrics"

// SpoolDir is the top-level spool directory.
//
// Not configurable: `postinst` creates it, `postrm` removes it, and the
// units list it in `ReadWritePaths=`. To relocate it, use a bind mount or
// a symlink.
const SpoolDir = "/var/spool/sonda"

// Duration is the type used to parse [time.Duration] safely. The decoder we use
// allows representing [time.Duration] as either integer or string, which leads to
// ambiguity and surprises; e.g., "6" meaning 6 nanoseconds not 6 hours. So, we
// define this intermediate type to prevent this source of ambiguity.
type Duration time.Duration

// Ensure that [Duration] implements [encoding.TextUnmarshaler].
var _ encoding.TextUnmarshaler = (*Duration)(nil)

// UnmarshalText implements [encoding.TextUnmarshaler].
func (d *Duration) UnmarshalText(text []byte) error {
	pv, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	if pv < 0 {
		return fmt.Errorf("expected zero or positive duration; got %v", pv)
	}
	*d = Duration(pv)
	return nil
}

// Settings contains `sonda` settings.
type Settings struct {
	Inetd Inetd `toml:"inetd"`
	Spool Spool `toml:"spool"`
}

// Inetd contains settings for the inetd-like servers.
type Inetd struct {
	Ptnop InetdPtnop `toml:"ptnop"`
}

// InetdPtnop contains `sonda-inetd-ptnop` settings.
type InetdPtnop struct {
	IdleTimeout Duration `toml:"idle-timeout"`
}

// Spool contains `sonda-spool` settings.
type Spool struct {
	GC SpoolGC `toml:"gc"`
}

// SpoolGC contains `sonda-spool gc` settings.
type SpoolGC struct {
	MaxAge Duration `toml:"max-age"`
}

// Defaults returns the default [*Settings].
func Defaults() *Settings {
	return &Settings{
		// The idle timeout is a guess: it should leave plenty of margin
		// to a client sending requests back to back, such as `sonda scan`.
		Inetd: Inetd{
			Ptnop: InetdPtnop{
				IdleTimeout: Duration(60 * time.Second),
			},
		},
		Spool: Spool{
			GC: SpoolGC{
				MaxAge: Duration(6 * time.Hour),
			},
		},
	}
}

// ReadInto reads the settings from the given config file path and stores them into the given
// [*Settings], which may have been previously initialized or customized. Typically, you
// create default [*Settings] using [Defaults] and then you modify them using this function,
// but YMMV. If the configuration file does not exist, we tolerate it and return nil.
func ReadInto(env *testable.Environ, path string, cfg *Settings) error {
	// Open the config file.
	filep, err := env.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // tolerate this error condition as documented
		}
		return fmt.Errorf("config: load error: %w", err)
	}
	defer filep.Close()

	// Parse the config file.
	decoder := toml.NewDecoder(filep)
	meta, err := decoder.Decode(cfg)
	if err != nil {
		return fmt.Errorf("config: parse error: %w", err)
	}

	// Reject the file if we have undecoded tags.
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return fmt.Errorf("config: validation error: unknown keys: %v", undecoded)
	}
	return nil
}
