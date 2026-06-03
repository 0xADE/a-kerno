package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/0xADE/a-kerno/internal/binparser"
	"github.com/0xADE/a-kerno/internal/daemon"
	"github.com/0xADE/a-kerno/internal/program"
	"github.com/0xADE/a-kerno/parser"
)

const (
	// errProgManagerNotInit is returned when program manager is not initialized.
	errProgManagerNotInit = "program manager not initialized"
	// stateFailed is the string representation of a failed state.
	stateFailed = "failed"
)

// handleListDaemons returns a formatted list of all managed daemons.
func handleListDaemons(s *Server, _ *parser.Command) string {
	daemons := s.manager.List()

	total := len(daemons)
	running := 0
	for _, d := range daemons {
		if d.IsRunning() {
			running++
		}
	}

	attrs := fmt.Sprintf("cmd: list-daemons\nstatus: 0\ntotal: %d\nrunning: %d\n", total, running)
	body := formatDaemonList(daemons)

	return parser.FormatOKWithBody(attrs, body)
}

// handleStatus returns detailed status for a specific daemon.
func handleStatus(s *Server, cmd *parser.Command) string {
	if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeString {
		return parser.FormatError("status requires a daemon name")
	}

	name := cmd.Args[0].Str
	d := s.manager.Get(name)
	if d == nil {
		return parser.FormatError(fmt.Sprintf("daemon %q not found", name))
	}

	attrs := formatDaemonStatus(d)
	return parser.FormatOKWithBody(attrs, "")
}

// handleRestart stops and then starts a daemon.
func handleRestart(s *Server, cmd *parser.Command) string {
	if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeString {
		return parser.FormatError("restart requires a daemon name")
	}

	name := cmd.Args[0].Str
	d := s.manager.Get(name)
	if d == nil {
		return parser.FormatError(fmt.Sprintf("daemon %q not found", name))
	}

	// Stop the daemon first.
	if err := s.manager.Stop(s.ctx, name); err != nil {
		return parser.FormatError(fmt.Sprintf("stop %q: %v", name, err))
	}

	// Start the daemon.
	if err := s.manager.Start(s.ctx, name); err != nil {
		return parser.FormatError(fmt.Sprintf("start %q: %v", name, err))
	}

	// Get updated daemon info.
	d = s.manager.Get(name)
	pid := 0
	if d != nil {
		pid = d.PID
	}

	attrs := fmt.Sprintf("cmd: restart\nname: %s\nstatus: 0\nnew_pid: %d\n", name, pid)
	return parser.FormatOKWithBody(attrs, "")
}

// handleStop stops a daemon and disables auto-restart.
func handleStop(s *Server, cmd *parser.Command) string {
	if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeString {
		return parser.FormatError("stop requires a daemon name")
	}

	name := cmd.Args[0].Str
	d := s.manager.Get(name)
	if d == nil {
		return parser.FormatError(fmt.Sprintf("daemon %q not found", name))
	}

	if err := s.manager.Stop(s.ctx, name); err != nil {
		return parser.FormatError(fmt.Sprintf("stop %q: %v", name, err))
	}

	// Set restart policy to disabled so monitorDaemon won't restart.
	d.SetRestartPolicy(daemon.RestartDisabled)

	// Mark feature as not ready.
	if s.features != nil {
		s.features.SetReady(name, false)
	}

	attrs := fmt.Sprintf("cmd: stop\nname: %s\nstatus: 0\n", name)
	return parser.FormatOKWithBody(attrs, "")
}

// handleStart starts a previously stopped daemon.
func handleStart(s *Server, cmd *parser.Command) string {
	if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeString {
		return parser.FormatError("start requires a daemon name")
	}

	name := cmd.Args[0].Str
	d := s.manager.Get(name)
	if d == nil {
		return parser.FormatError(fmt.Sprintf("daemon %q not found", name))
	}

	if err := s.manager.Start(s.ctx, name); err != nil {
		return parser.FormatError(fmt.Sprintf("start %q: %v", name, err))
	}

	// Get updated info.
	d = s.manager.Get(name)
	pid := 0
	if d != nil {
		pid = d.PID
	}

	// Mark feature as ready.
	if s.features != nil {
		s.features.SetReady(name, true)
	}

	attrs := fmt.Sprintf("cmd: start\nname: %s\nstatus: 0\npid: %d\n", name, pid)
	return parser.FormatOKWithBody(attrs, "")
}

// handleListFeatures returns the feature registry as a formatted list.
func handleListFeatures(s *Server, _ *parser.Command) string {
	if s.features == nil {
		return parser.FormatError("feature registry not initialized")
	}

	features := s.features.List()
	ready := 0
	failed := 0
	for _, f := range features {
		if f.Ready {
			ready++
		} else {
			failed++
		}
	}

	attrs := fmt.Sprintf("cmd: list-features\nstatus: 0\nfeatures: %d\nready: %d\nfailed: %d\n",
		len(features), ready, failed)

	var bodyLines []string
	for _, f := range features {
		state := stateFailed
		extra := ""
		if f.Ready {
			state = "ready"
		}
		d := s.manager.Get(f.Name)
		if d != nil {
			if d.PID > 0 {
				extra = fmt.Sprintf("pid=%d", d.PID)
			}
			if d.IsRunning() {
				uptime := d.Uptime().Truncate(time.Second)
				extra += fmt.Sprintf(" uptime=%v", uptime)
			}
		}
		line := fmt.Sprintf("%-16s %-10s %s", f.Name, state, extra)
		bodyLines = append(bodyLines, line)
	}
	body := strings.Join(bodyLines, "\n")

	return parser.FormatOKWithBody(attrs, body)
}

// handleLogs returns recent log lines for a daemon.
// Expects args: [0] = daemon name (string), [1] = line count (int, optional, default 50).
func handleLogs(s *Server, cmd *parser.Command) string {
	// Determine the daemon name from the first argument.
	if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeString {
		return parser.FormatError("logs requires a daemon name (arg[0])")
	}
	name := cmd.Args[0].Str

	// Determine the requested line count from the second argument (optional).
	lines := 50
	if len(cmd.Args) > 1 && cmd.Args[1].Type == parser.TypeInt {
		if n := int(cmd.Args[1].Int); n > 0 {
			lines = n
		}
	}

	d := s.manager.Get(name)
	if d == nil {
		return parser.FormatError(fmt.Sprintf("daemon %q not found", name))
	}

	if d.Logs == nil {
		return parser.FormatError(fmt.Sprintf("no log buffer for daemon %q", name))
	}

	tail := d.Logs.Tail(lines)

	attrs := fmt.Sprintf("cmd: logs\nname: %s\nstatus: 0\nlines: %d\n", name, len(tail))
	body := strings.Join(tail, "\n")

	return parser.FormatOKWithBody(attrs, body)
}

// handleShutdown triggers a graceful shutdown of a-kerno.
func handleShutdown(s *Server, _ *parser.Command) string {
	attrs := "cmd: shutdown\nstatus: 0\nmsg: shutting down all daemons\n"
	s.cancel()
	return parser.FormatOKWithBody(attrs, "")
}

// ---------------------------------------------------------------------------
// Program management handlers (Phase 5)
// ---------------------------------------------------------------------------

// handleListPrograms returns a formatted list of all user programs.
func handleListPrograms(s *Server, _ *parser.Command) string {
	if s.pm == nil {
		return parser.FormatError(errProgManagerNotInit)
	}

	progs := s.pm.List()

	total := len(progs)
	running := 0
	failed := 0
	disabled := 0
	for _, p := range progs {
		switch p.GetState() {
		case program.ProgRunning, program.ProgHealthy:
			running++
		case program.ProgFailed, program.ProgHealthFailed:
			failed++
		case program.ProgDisabled:
			disabled++
		}
	}

	attrs := fmt.Sprintf("cmd: prog-list\nstatus: 0\ntotal: %d\nrunning: %d\nfailed: %d\ndisabled: %d\n",
		total, running, failed, disabled)

	var bodyLines []string
	for _, p := range progs {
		pid := p.PID
		state := p.GetState().String()
		phase := p.Config.Phase
		if phase == "" {
			phase = "post"
		}
		line := fmt.Sprintf("%-20s %6s  %-16s phase=%-6s pid=%d",
			p.Name, state, state, phase, pid)
		bodyLines = append(bodyLines, line)
	}
	body := strings.Join(bodyLines, "\n")

	return parser.FormatOKWithBody(attrs, body)
}

// handleProgramStatus returns detailed status for a specific program.
func handleProgramStatus(s *Server, cmd *parser.Command) string {
	if s.pm == nil {
		return parser.FormatError(errProgManagerNotInit)
	}

	if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeString {
		return parser.FormatError("prog-status requires a program name")
	}

	name := cmd.Args[0].Str
	prog := s.pm.Get(name)
	if prog == nil {
		return parser.FormatError(fmt.Sprintf("program %q not found", name))
	}

	p := prog
	state := p.GetState().String()
	exitCode := p.ExitCode
	pid := p.PID
	health := healthStatusToString(p.Health)

	uptime := "-"
	if p.IsRunning() && !p.StartedAt.IsZero() {
		uptime = time.Since(p.StartedAt).Truncate(time.Second).String()
	}

	attrs := fmt.Sprintf(
		"cmd: prog-status\n"+
			"name: %s\n"+
			"status: 0\n"+
			"state: %s\n"+
			"pid: %d\n"+
			"exit_code: %d\n"+
			"uptime: %s\n"+
			"health: %s\n"+
			"phase: %s\n"+
			"priority: %d\n"+
			"exec: %s\n",
		p.Name, state, pid, exitCode, uptime,
		health, p.Config.Phase, p.Config.Priority, p.Config.Exec,
	)

	return parser.FormatOKWithBody(attrs, "")
}

// handleStartProgram starts a program by name.
func handleStartProgram(s *Server, cmd *parser.Command) string {
	if s.pm == nil {
		return parser.FormatError(errProgManagerNotInit)
	}

	if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeString {
		return parser.FormatError("prog-start requires a program name")
	}

	name := cmd.Args[0].Str
	if err := s.pm.StartProgram(s.ctx, name); err != nil {
		return parser.FormatError(fmt.Sprintf("start program %q: %v", name, err))
	}

	prog := s.pm.Get(name)
	pid := 0
	if prog != nil {
		pid = prog.PID
	}

	attrs := fmt.Sprintf("cmd: prog-start\nname: %s\nstatus: 0\npid: %d\n", name, pid)
	return parser.FormatOKWithBody(attrs, "")
}

// handleStopProgram stops a program by name.
func handleStopProgram(s *Server, cmd *parser.Command) string {
	if s.pm == nil {
		return parser.FormatError(errProgManagerNotInit)
	}

	if len(cmd.Args) == 0 || cmd.Args[0].Type != parser.TypeString {
		return parser.FormatError("prog-stop requires a program name")
	}

	name := cmd.Args[0].Str
	if err := s.pm.StopProgram(s.ctx, name); err != nil {
		return parser.FormatError(fmt.Sprintf("stop program %q: %v", name, err))
	}

	attrs := fmt.Sprintf("cmd: prog-stop\nname: %s\nstatus: 0\n", name)
	return parser.FormatOKWithBody(attrs, "")
}

// healthStatusToString converts a program.HealthStatus to a string.
func healthStatusToString(h program.HealthStatus) string {
	switch h {
	case program.HealthUnknown:
		return "unknown"
	case program.HealthChecking:
		return "checking"
	case program.HealthOK:
		return "ok"
	case program.HealthFailed:
		return "failed"
	default:
		return fmt.Sprintf("unknown(%d)", h)
	}
}

// Ensure unused import is referenced (for strconv used by potential future expansions).
var _ = strconv.Itoa

// ---------------------------------------------------------------------------
// BIN01 binary protocol handlers (Phase 6)
// ---------------------------------------------------------------------------

// binAttr extracts a named string attribute from a BinCommand.
func binAttr(cmd *binparser.BinCommand, key string) (string, bool) {
	if cmd.Attrs == nil {
		return "", false
	}
	v, ok := cmd.Attrs[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// binAttrName extracts the "name" attribute from a BinCommand.
func binAttrName(cmd *binparser.BinCommand) (string, bool) { return binAttr(cmd, "name") }

// binAttrInt extracts an int64 attribute from a BinCommand.
func binAttrInt(cmd *binparser.BinCommand, key string) (int64, bool) {
	if cmd.Attrs == nil {
		return 0, false
	}
	v, ok := cmd.Attrs[key]
	if !ok {
		return 0, false
	}
	i, ok := v.(int64)
	return i, ok
}

// handleBinListDaemons handles the binary list-daemons command.
func handleBinListDaemons(s *Server, _ *binparser.BinCommand) (byte, string) {
	daemons := s.manager.List()
	total := len(daemons)
	running := 0
	for _, d := range daemons {
		if d.IsRunning() {
			running++
		}
	}
	msg := fmt.Sprintf("total=%d running=%d\n%s", total, running, formatDaemonList(daemons))
	return binparser.CodeOK, msg
}

// handleBinStatus handles the binary status command.
func handleBinStatus(s *Server, cmd *binparser.BinCommand) (byte, string) {
	name, ok := binAttrName(cmd)
	if !ok || name == "" {
		return binparser.CodeError, "status requires a daemon name"
	}
	d := s.manager.Get(name)
	if d == nil {
		return binparser.CodeError, fmt.Sprintf("daemon %q not found", name)
	}
	return binparser.CodeOK, formatDaemonStatus(d)
}

// handleBinRestart handles the binary restart command.
func handleBinRestart(s *Server, cmd *binparser.BinCommand) (byte, string) {
	name, ok := binAttrName(cmd)
	if !ok || name == "" {
		return binparser.CodeError, "restart requires a daemon name"
	}
	d := s.manager.Get(name)
	if d == nil {
		return binparser.CodeError, fmt.Sprintf("daemon %q not found", name)
	}
	if err := s.manager.Stop(s.ctx, name); err != nil {
		return binparser.CodeError, fmt.Sprintf("stop %q: %v", name, err)
	}
	if err := s.manager.Start(s.ctx, name); err != nil {
		return binparser.CodeError, fmt.Sprintf("start %q: %v", name, err)
	}
	d = s.manager.Get(name)
	pid := 0
	if d != nil {
		pid = d.PID
	}
	return binparser.CodeOK, fmt.Sprintf("daemon %q restarted (pid=%d)", name, pid)
}

// handleBinStop handles the binary stop command.
func handleBinStop(s *Server, cmd *binparser.BinCommand) (byte, string) {
	name, ok := binAttrName(cmd)
	if !ok || name == "" {
		return binparser.CodeError, "stop requires a daemon name"
	}
	d := s.manager.Get(name)
	if d == nil {
		return binparser.CodeError, fmt.Sprintf("daemon %q not found", name)
	}
	if err := s.manager.Stop(s.ctx, name); err != nil {
		return binparser.CodeError, fmt.Sprintf("stop %q: %v", name, err)
	}
	d.SetRestartPolicy(daemon.RestartDisabled)
	if s.features != nil {
		s.features.SetReady(name, false)
	}
	return binparser.CodeOK, fmt.Sprintf("daemon %q stopped", name)
}

// handleBinStart handles the binary start command.
func handleBinStart(s *Server, cmd *binparser.BinCommand) (byte, string) {
	name, ok := binAttrName(cmd)
	if !ok || name == "" {
		return binparser.CodeError, "start requires a daemon name"
	}
	d := s.manager.Get(name)
	if d == nil {
		return binparser.CodeError, fmt.Sprintf("daemon %q not found", name)
	}
	if err := s.manager.Start(s.ctx, name); err != nil {
		return binparser.CodeError, fmt.Sprintf("start %q: %v", name, err)
	}
	d = s.manager.Get(name)
	pid := 0
	if d != nil {
		pid = d.PID
	}
	if s.features != nil {
		s.features.SetReady(name, true)
	}
	return binparser.CodeOK, fmt.Sprintf("daemon %q started (pid=%d)", name, pid)
}

// handleBinListFeatures handles the binary list-features command.
func handleBinListFeatures(s *Server, _ *binparser.BinCommand) (byte, string) {
	if s.features == nil {
		return binparser.CodeError, "feature registry not initialized"
	}
	features := s.features.List()
	ready := 0
	for _, f := range features {
		if f.Ready {
			ready++
		}
	}
	var lines []string
	for _, f := range features {
		state := stateFailed
		if f.Ready {
			state = "ready"
		}
		lines = append(lines, fmt.Sprintf("%s %s (v=%s)", f.Name, state, f.Version))
	}
	msg := fmt.Sprintf("features=%d ready=%d\n%s", len(features), ready, joinLines(lines))
	return binparser.CodeOK, msg
}

// handleBinLogs handles the binary logs command.
func handleBinLogs(s *Server, cmd *binparser.BinCommand) (byte, string) {
	name, ok := binAttrName(cmd)
	if !ok || name == "" {
		return binparser.CodeError, "logs requires a daemon name"
	}
	lines := int64(50)
	if n, ok := binAttrInt(cmd, "lines"); ok && n > 0 {
		lines = n
	}
	d := s.manager.Get(name)
	if d == nil {
		return binparser.CodeError, fmt.Sprintf("daemon %q not found", name)
	}
	if d.Logs == nil {
		return binparser.CodeError, fmt.Sprintf("no log buffer for daemon %q", name)
	}
	tail := d.Logs.Tail(int(lines))
	return binparser.CodeOK, fmt.Sprintf("lines=%d\n%s", len(tail), joinLines(tail))
}

// handleBinShutdown handles the binary shutdown command.
func handleBinShutdown(s *Server, _ *binparser.BinCommand) (byte, string) {
	s.cancel()
	return binparser.CodeOK, "shutting down all daemons"
}

// handleBinListPrograms handles the binary list-programs command.
func handleBinListPrograms(s *Server, _ *binparser.BinCommand) (byte, string) {
	if s.pm == nil {
		return binparser.CodeError, errProgManagerNotInit
	}
	progs := s.pm.List()
	total := len(progs)
	running := 0
	failed := 0
	for _, p := range progs {
		switch p.GetState() {
		case program.ProgRunning, program.ProgHealthy:
			running++
		case program.ProgFailed, program.ProgHealthFailed:
			failed++
		}
	}
	var lines []string
	for _, p := range progs {
		phase := p.Config.Phase
		if phase == "" {
			phase = "post"
		}
		lines = append(lines, fmt.Sprintf("%s state=%s phase=%s pid=%d",
			p.Name, p.GetState().String(), phase, p.PID))
	}
	msg := fmt.Sprintf("total=%d running=%d failed=%d\n%s", total, running, failed, joinLines(lines))
	return binparser.CodeOK, msg
}

// handleBinProgramStatus handles the binary program-status command.
func handleBinProgramStatus(s *Server, cmd *binparser.BinCommand) (byte, string) {
	if s.pm == nil {
		return binparser.CodeError, errProgManagerNotInit
	}
	name, ok := binAttrName(cmd)
	if !ok || name == "" {
		return binparser.CodeError, "prog-status requires a program name"
	}
	prog := s.pm.Get(name)
	if prog == nil {
		return binparser.CodeError, fmt.Sprintf("program %q not found", name)
	}
	p := prog
	msg := fmt.Sprintf(
		"name=%s state=%s pid=%d exit_code=%d health=%s phase=%s priority=%d exec=%s",
		p.Name, p.GetState().String(), p.PID, p.ExitCode,
		healthStatusToString(p.Health), p.Config.Phase, p.Config.Priority, p.Config.Exec,
	)
	return binparser.CodeOK, msg
}

// handleBinStartProgram handles the binary start-program command.
func handleBinStartProgram(s *Server, cmd *binparser.BinCommand) (byte, string) {
	if s.pm == nil {
		return binparser.CodeError, errProgManagerNotInit
	}
	name, ok := binAttrName(cmd)
	if !ok || name == "" {
		return binparser.CodeError, "prog-start requires a program name"
	}
	if err := s.pm.StartProgram(s.ctx, name); err != nil {
		return binparser.CodeError, fmt.Sprintf("start program %q: %v", name, err)
	}
	prog := s.pm.Get(name)
	pid := 0
	if prog != nil {
		pid = prog.PID
	}
	return binparser.CodeOK, fmt.Sprintf("program %q started (pid=%d)", name, pid)
}

// handleBinStopProgram handles the binary stop-program command.
func handleBinStopProgram(s *Server, cmd *binparser.BinCommand) (byte, string) {
	if s.pm == nil {
		return binparser.CodeError, errProgManagerNotInit
	}
	name, ok := binAttrName(cmd)
	if !ok || name == "" {
		return binparser.CodeError, "prog-stop requires a program name"
	}
	if err := s.pm.StopProgram(s.ctx, name); err != nil {
		return binparser.CodeError, fmt.Sprintf("stop program %q: %v", name, err)
	}
	return binparser.CodeOK, fmt.Sprintf("program %q stopped", name)
}

// joinLines joins a slice of strings with newlines.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	result := lines[0]
	for i := 1; i < len(lines); i++ {
		result += "\n" + lines[i]
	}
	return result
}
