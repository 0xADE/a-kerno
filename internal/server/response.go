package server

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/0xADE/a-kerno/internal/daemon"
)

// formatDaemonList formats a slice of daemons as a human-readable table
// for the list-daemons command response body.
func formatDaemonList(daemons []*daemon.Daemon) string {
	if len(daemons) == 0 {
		return "(no daemons configured)"
	}

	// Sort by name for deterministic output.
	sort.Slice(daemons, func(i, j int) bool {
		return daemons[i].Name < daemons[j].Name
	})

	var lines []string
	for _, d := range daemons {
		state := daemonStateToString(d.State)
		pid := fmt.Sprintf("%d", d.PID)
		if d.PID == 0 {
			pid = "0"
		}

		uptime := "-"
		if d.State == daemon.DaemonRunning && !d.StartedAt.IsZero() {
			uptime = time.Since(d.StartedAt).Truncate(time.Second).String()
		}

		restarts := fmt.Sprintf("%d", d.RestartCount)
		policy := string(d.Config.Restart)

		lines = append(lines, fmt.Sprintf("%-16s %6s  %-10s uptime=%-10s restarts=%s policy=%s",
			d.Name, pid, state, uptime, restarts, policy))
	}

	return strings.Join(lines, "\n")
}

// formatDaemonStatus formats a single daemon as a detailed status block
// for the status command response attributes.
func formatDaemonStatus(d *daemon.Daemon) string {
	state := daemonStateToString(d.State)

	exitCode := -1
	if d.State == daemon.DaemonFailed || d.State == daemon.DaemonStopped {
		// When stopped/failed, exit code may not be available; show -1.
		exitCode = -1
	}

	pid := d.PID
	pgid := d.PGID

	uptime := "-"
	if d.State == daemon.DaemonRunning && !d.StartedAt.IsZero() {
		uptime = time.Since(d.StartedAt).Truncate(time.Second).String()
	}

	socket := d.Config.Socket
	socketOK := "false"
	if d.State == daemon.DaemonRunning && socket != "" {
		socketOK = "true"
	}

	return fmt.Sprintf(
		"cmd: status\n"+
			"name: %s\n"+
			"status: %s\n"+
			"pid: %d\n"+
			"pgid: %d\n"+
			"exit_code: %d\n"+
			"uptime: %s\n"+
			"restarts: %d\n"+
			"restart_policy: %s\n"+
			"order: %d\n"+
			"socket: %s\n"+
			"socket_ok: %s\n"+
			"exec: %s\n",
		d.Name, state, pid, pgid, exitCode, uptime,
		d.RestartCount, d.Config.Restart, d.Config.Order,
		socket, socketOK, d.Config.Exec,
	)
}

// daemonStateToString converts a DaemonState to its string representation.
func daemonStateToString(s daemon.DaemonState) string {
	switch s {
	case daemon.DaemonStopped:
		return "stopped"
	case daemon.DaemonStarting:
		return "starting"
	case daemon.DaemonRunning:
		return "running"
	case daemon.DaemonFailed:
		return stateFailed
	case daemon.DaemonDisabled:
		return "disabled"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}
