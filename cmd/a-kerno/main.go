// Package main is the entry point for a-kerno, the ADE daemon orchestrator.
// It reads the declarative configuration ~/.config/ade/daemons.md, launches
// daemons as child processes, starts user programs from autostart directories,
// and coordinates the full startup/shutdown lifecycle through the Orchestrator.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"os/user"
	"syscall"

	"github.com/0xADE/a-kerno/internal/config"
	"github.com/0xADE/a-kerno/internal/daemon"
	"github.com/0xADE/a-kerno/internal/feature"
	"github.com/0xADE/a-kerno/internal/orchestrator"
	"github.com/0xADE/a-kerno/internal/program"
	"github.com/0xADE/a-kerno/internal/server"
)

func main() {
	os.Exit(run())
}

// run encapsulates the main logic and returns an exit code.
func run() int {
	// 1. Initialize configuration (reads environment variables, expands paths).
	if err := config.Init(); err != nil {
		slog.Error("failed to initialize configuration", "error", err)
		return 1
	}
	cfg := config.Get()

	slog.Info("a-kerno starting",
		"config_home", cfg.ConfigHome,
		"runtime_dir", cfg.RuntimeDir,
		"kerno_sock", cfg.KernoSock,
		"daemons_md", cfg.DaemonsMD,
		"autostart_dir", cfg.AutostartDir,
	)

	// 2. Resolve user identity for variable expansion.
	currentUser, err := user.Current()
	if err != nil {
		slog.Error("failed to resolve current user", "error", err)
		return 1
	}
	uid := currentUser.Uid
	home := currentUser.HomeDir

	// 3. Load daemon configurations from daemons.md.
	//    If the file does not exist, a template is created automatically and
	//    a-kerno starts with an empty daemon list (warning, not fatal).
	daemonConfigs, err := daemon.LoadConfig(cfg.DaemonsMD, uid, home)
	if err != nil {
		slog.Warn("failed to load daemon configuration, starting with empty daemon list",
			"path", cfg.DaemonsMD, "error", err,
		)
		daemonConfigs = nil
	}

	slog.Info("loaded daemon configurations", "count", len(daemonConfigs))
	for _, dc := range daemonConfigs {
		slog.Info("daemon config",
			"name", dc.Name,
			"exec", dc.Exec,
			"order", dc.Order,
			"enabled", dc.Enabled,
			"restart", dc.Restart,
			"socket", dc.Socket,
		)
	}

	// 4. Load program configurations from autostart directories.
	xdgAutostartDir := home + "/.config/autostart"
	programConfigs, err := program.LoadProgramConfigs(cfg.AutostartDir, xdgAutostartDir, uid, home)
	if err != nil {
		slog.Warn("failed to load program configurations (continuing without)", "error", err)
		programConfigs = nil
	}

	slog.Info("loaded program configurations", "count", len(programConfigs))
	for _, pc := range programConfigs {
		slog.Info("program config",
			"name", pc.Name,
			"exec", pc.Exec,
			"phase", pc.Phase,
			"priority", pc.Priority,
			"enabled", pc.Enabled,
			"source", pc.Source,
		)
	}

	// 5. Create the feature registry.
	feats := feature.NewRegistry()

	// 6. Create the daemon manager.
	dm := daemon.NewDaemonManager(cfg, daemonConfigs, uid, home)

	// 7. Create the program manager.
	pm := program.NewProgramManager(programConfigs)

	// 8. Create the management Unix socket server.
	srv := server.NewServer(cfg, dm, feats, pm)

	// 9. Create the orchestrator.
	orch := orchestrator.NewOrchestrator(cfg, dm, pm, feats, srv)

	// 10. Set up signal handling: SIGINT, SIGTERM for graceful shutdown.
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT, syscall.SIGTERM,
	)
	defer cancel()

	// 11. Start the management server BEFORE daemons so the socket is
	//     available for daemons that need to connect to a-kerno during init.
	if err := srv.Start(ctx); err != nil {
		slog.Error("failed to start management server", "error", err)
		return 1
	}

	// 12. Run the orchestrator: early programs → daemons → post programs.
	if err := orch.Run(ctx); err != nil {
		slog.Error("orchestrator failed to start", "error", err)
		_ = dm.StopAll(context.Background())
		return 1
	}

	// 13. Start fsnotify watcher for daemons.md auto-reload.
	if err := dm.WatchConfig(ctx); err != nil {
		slog.Warn("failed to start config watcher (continuing without)", "error", err)
	}

	slog.Info("all daemons and programs started, waiting for shutdown signal",
		"features", feats.ExportEnv(),
	)

	// 14. Wait for termination: either OS signal or shutdown via management socket.
	select {
	case <-ctx.Done():
		slog.Info("received OS shutdown signal")
	case <-srv.ShutdownC():
		slog.Info("shutdown command received via management socket")
	}

	slog.Info("stopping a-kerno gracefully")

	// 15. Stop the management server (stop accepting new connections).
	srv.Stop()

	// 16. Orchestrator shutdown: post programs → daemons → early programs.
	if err := orch.Shutdown(context.Background()); err != nil {
		slog.Error("errors during shutdown", "error", err)
		return 1
	}

	slog.Info("a-kerno exited successfully")
	return 0
}
