// Command satellites-agent is the satellites worker binary that pulls tasks
// from the queue. On boot it logs a single identifying line through arbor;
// later stories replace this stub with the task loop.
package main

import (
	"os"

	satarbor "github.com/bobmcallan/satellites/internal/arbor"
	"github.com/bobmcallan/satellites/internal/config"
)

func main() {
	cfg, cfgWarnings := config.Load()

	logger := satarbor.New(cfg.LogLevel)
	logger.Info().
		Str("binary", "satellites-agent").
		Str("version", config.Version).
		Str("build", config.Build).
		Str("commit", config.GitCommit).
		Str("env", cfg.Env).
		Str("fly_machine_id", os.Getenv("FLY_MACHINE_ID")).
		Msgf("satellites-agent %s", config.GetFullVersion())

	for _, w := range cfgWarnings {
		logger.Warn().Str("warning", w).Msg("config: startup warning")
	}
}
