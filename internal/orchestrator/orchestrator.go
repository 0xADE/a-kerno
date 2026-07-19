// Package orchestrator provides the top-level Orchestrator that coordinates
// the startup and shutdown sequence of a-kerno: early programs → daemons →
// feature registration → session WM → post programs.
package orchestrator

import (
	"context"
	"log/slog"

	"github.com/0xADE/a-kerno/internal/config"
	"github.com/0xADE/a-kerno/internal/daemon"
	"github.com/0xADE/a-kerno/internal/feature"
	"github.com/0xADE/a-kerno/internal/program"
	"github.com/0xADE/a-kerno/internal/server"
	"github.com/0xADE/a-kerno/internal/sessionwm"
)

// Orchestrator coordinates the startup and shutdown of all a-kerno
// subsystems in the correct order.
type Orchestrator struct {
	cfg      *config.Config
	dm       *daemon.DaemonManager
	pm       *program.ProgramManager
	wm       *sessionwm.Manager
	features *feature.Registry
	server   *server.Server
	logger   *slog.Logger
}

// NewOrchestrator creates a new Orchestrator with the given dependencies.
func NewOrchestrator(
	cfg *config.Config,
	dm *daemon.DaemonManager,
	pm *program.ProgramManager,
	wm *sessionwm.Manager,
	features *feature.Registry,
	srv *server.Server,
) *Orchestrator {

	return &Orchestrator{
		cfg:      cfg,
		dm:       dm,
		pm:       pm,
		wm:       wm,
		features: features,
		server:   srv,
		logger:   slog.Default().With("component", "orchestrator"),
	}
}

// Run executes the startup sequence:
//  1. Start early-phase programs.
//  2. Start all enabled daemons.
//  3. Register daemon features.
//  4. Start session WM / compositor.
//  5. Start post-phase programs.
func (o *Orchestrator) Run(ctx context.Context) error {
	// 1. Start early-phase programs.
	o.logger.Info("starting early-phase programs")
	if err := o.pm.StartPhase(ctx, "early"); err != nil {
		o.logger.Error("failed to start early programs", "error", err)
		// Early program failure is non-fatal; continue.
	}

	// 2. Start all daemons.
	o.logger.Info("starting all enabled daemons")
	if err := o.dm.StartAll(ctx); err != nil {
		return err
	}

	// 3. Register daemon features and mark them ready.
	daemons := o.dm.List()
	for _, d := range daemons {
		if d.Config.Enabled {
			version := "0.0.0" // placeholder; discovered later
			o.features.Register(d.Name, version, d.Config.Socket)
			if d.IsRunning() {
				o.features.SetReady(d.Name, true)
			}
		}
	}

	// 4. Start session WM (Hyprland / i3 / …). Failure is fatal when configured.
	o.logger.Info("starting session WM")
	if err := o.wm.Start(ctx); err != nil {
		return err
	}

	// 5. Start post-phase programs.
	o.logger.Info("starting post-phase programs")
	if err := o.pm.StartPhase(ctx, "post"); err != nil {
		o.logger.Error("failed to start post programs", "error", err)
		// Post program failure is non-fatal.
	}

	o.logger.Info("orchestrator startup complete",
		"features", o.features.ExportEnv(),
		"session_wm", o.wm.Spec(),
	)

	return nil
}

// Shutdown executes the graceful shutdown sequence:
//  1. Stop post-phase programs.
//  2. Stop session WM.
//  3. Stop all daemons.
//  4. Stop early-phase programs.
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.logger.Info("orchestrator shutdown initiated")

	o.logger.Info("stopping post-phase programs")
	if err := o.pm.StopPhase(ctx, "post"); err != nil {
		o.logger.Error("errors stopping post programs", "error", err)
	}

	o.logger.Info("stopping session WM")
	if err := o.wm.Stop(ctx); err != nil {
		o.logger.Error("errors stopping session WM", "error", err)
	}

	o.logger.Info("stopping all daemons")
	if err := o.dm.StopAll(ctx); err != nil {
		o.logger.Error("errors stopping daemons", "error", err)
	}

	o.logger.Info("stopping early-phase programs")
	if err := o.pm.StopPhase(ctx, "early"); err != nil {
		o.logger.Error("errors stopping early programs", "error", err)
	}

	o.logger.Info("orchestrator shutdown complete")
	return nil
}
