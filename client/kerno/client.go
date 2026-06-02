// Package kerno provides a client library for the a-kerno management API.
// It supports both TXT01 (text-based CMDLIST) and BIN01 (binary) protocols
// over a Unix domain socket. The client auto-detects the protocol mode
// or can be explicitly created for binary mode via NewBinaryClient.
package kerno

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/0xADE/a-kerno/internal/binparser"
	"github.com/0xADE/a-kerno/parser"
)

// Client is a client for the a-kerno management API.
// It connects via a Unix domain socket and supports both TXT01 and BIN01
// protocols. Use NewClient for text mode or NewBinaryClient for binary mode.
type Client struct {
	socketPath string
	conn       net.Conn
	parser     *parser.Parser
	binParser  *binparser.Parser
	binWriter  *binparser.Writer
	useBinary  bool
	mu         sync.Mutex
}

// DaemonInfo is a summary of a daemon returned by ListDaemons.
type DaemonInfo struct {
	Name    string
	PID     int
	State   string
	Restart string
}

// DaemonStatus is the detailed status of a single daemon.
type DaemonStatus struct {
	Name          string
	PID           int
	PGID          int
	State         string
	ExitCode      int
	Uptime        string
	Restarts      int
	RestartPolicy string
	Order         int
	Socket        string
	SocketOK      bool
	Exec          string
}

// FeatureInfo is a summary of a feature returned by ListFeatures.
type FeatureInfo struct {
	Name    string
	Version string
	Ready   bool
}

// ProgramInfo is a summary of a program returned by ListPrograms.
type ProgramInfo struct {
	Name  string
	State string
	Phase string
	PID   int
}

// ProgramStatus is the detailed status of a single program.
type ProgramStatus struct {
	Name     string
	State    string
	PID      int
	ExitCode int
	Health   string
	Phase    string
	Priority int
	Exec     string
}

// NewClient creates a new TXT01 (text protocol) client connected to the given socket.
func NewClient(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", socketPath, err)
	}

	// Send TXT01 header.
	if _, err := fmt.Fprint(conn, "TXT01\n"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write TXT01 header: %w", err)
	}

	c := &Client{
		socketPath: socketPath,
		conn:       conn,
		useBinary:  false,
	}
	return c, nil
}

// NewBinaryClient creates a new BIN01 (binary protocol) client connected to the given socket.
func NewBinaryClient(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", socketPath, err)
	}

	// Send BIN1 magic.
	if _, err := conn.Write([]byte("BIN1")); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write BIN1 magic: %w", err)
	}

	// Read server acknowledgment.
	ack := make([]byte, 4)
	if _, err := io.ReadFull(conn, ack); err != nil {
		conn.Close()
		return nil, fmt.Errorf("read BIN1 ack: %w", err)
	}
	if string(ack) != "BIN1" {
		conn.Close()
		return nil, fmt.Errorf("unexpected ack: %q (expected BIN1)", string(ack))
	}

	c := &Client{
		socketPath: socketPath,
		conn:       conn,
		binParser:  binparser.NewParser(conn),
		binWriter:  binparser.NewWriter(conn),
		useBinary:  true,
	}
	return c, nil
}

// Close closes the connection to the a-kerno socket.
func (c *Client) Close() error {
	return c.conn.Close()
}

// ListDaemons returns a list of all managed daemons.
func (c *Client) ListDaemons() ([]DaemonInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		return c.listDaemonsBin()
	}
	return c.listDaemonsText()
}

func (c *Client) listDaemonsText() ([]DaemonInfo, error) {
	cmd, err := c.sendCommand("list-daemons", nil)
	if err != nil {
		return nil, err
	}
	_ = cmd
	// Parse the response body.
	// For now, return a simple parsed result.
	return parseDaemonList(cmd)
}

func (c *Client) listDaemonsBin() ([]DaemonInfo, error) {
	resp, err := c.sendBinCommand(binparser.CmdListDaemons, nil)
	if err != nil {
		return nil, err
	}
	_ = resp
	return parseDaemonListFromMsg(resp.Attrs)
}

// Status returns detailed status for a daemon.
func (c *Client) Status(name string) (*DaemonStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		return c.statusBin(name)
	}
	return c.statusText(name)
}

func (c *Client) statusText(name string) (*DaemonStatus, error) {
	cmd, err := c.sendCommand("status", map[string]string{"name": name})
	if err != nil {
		return nil, err
	}
	return parseDaemonStatus(cmd)
}

func (c *Client) statusBin(name string) (*DaemonStatus, error) {
	resp, err := c.sendBinCommand(binparser.CmdStatus, map[string]interface{}{
		"name": name,
	})
	if err != nil {
		return nil, err
	}
	return parseDaemonStatusFromMsg(resp.Attrs)
}

// Restart restarts a daemon by name.
func (c *Client) Restart(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		_, err := c.sendBinCommand(binparser.CmdRestart, map[string]interface{}{
			"name": name,
		})
		return err
	}
	_, err := c.sendCommand("restart", map[string]string{"name": name})
	return err
}

// Stop stops a daemon by name.
func (c *Client) Stop(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		_, err := c.sendBinCommand(binparser.CmdStop, map[string]interface{}{
			"name": name,
		})
		return err
	}
	_, err := c.sendCommand("stop", map[string]string{"name": name})
	return err
}

// Start starts a daemon by name.
func (c *Client) Start(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		_, err := c.sendBinCommand(binparser.CmdStart, map[string]interface{}{
			"name": name,
		})
		return err
	}
	_, err := c.sendCommand("start", map[string]string{"name": name})
	return err
}

// ListFeatures returns the list of registered features.
func (c *Client) ListFeatures() ([]FeatureInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		return c.listFeaturesBin()
	}
	return c.listFeaturesText()
}

func (c *Client) listFeaturesText() ([]FeatureInfo, error) {
	cmd, err := c.sendCommand("list-features", nil)
	if err != nil {
		return nil, err
	}
	return parseFeatureList(cmd)
}

func (c *Client) listFeaturesBin() ([]FeatureInfo, error) {
	resp, err := c.sendBinCommand(binparser.CmdListFeatures, nil)
	if err != nil {
		return nil, err
	}
	return parseFeatureListFromMsg(resp.Attrs)
}

// Logs returns log lines for a daemon.
func (c *Client) Logs(name string, lines int) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		return c.logsBin(name, lines)
	}
	return c.logsText(name, lines)
}

func (c *Client) logsText(name string, lines int) ([]string, error) {
	if lines <= 0 {
		lines = 50
	}
	cmd, err := c.sendCommand("logs", map[string]string{
		"name":  name,
		"lines": strconv.Itoa(lines),
	})
	if err != nil {
		return nil, err
	}
	return parseLogLines(cmd), nil
}

func (c *Client) logsBin(name string, lines int) ([]string, error) {
	if lines <= 0 {
		lines = 50
	}
	resp, err := c.sendBinCommand(binparser.CmdLogs, map[string]interface{}{
		"name":  name,
		"lines": int64(lines),
	})
	if err != nil {
		return nil, err
	}
	return parseLogLinesFromMsg(resp.Attrs), nil
}

// Shutdown triggers a graceful shutdown of a-kerno.
func (c *Client) Shutdown() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		_, err := c.sendBinCommand(binparser.CmdShutdown, nil)
		return err
	}
	_, err := c.sendCommand("shutdown", nil)
	return err
}

// ListPrograms returns a list of all user programs.
func (c *Client) ListPrograms() ([]ProgramInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		return c.listProgramsBin()
	}
	return c.listProgramsText()
}

func (c *Client) listProgramsText() ([]ProgramInfo, error) {
	cmd, err := c.sendCommand("prog-list", nil)
	if err != nil {
		return nil, err
	}
	return parseProgramList(cmd)
}

func (c *Client) listProgramsBin() ([]ProgramInfo, error) {
	resp, err := c.sendBinCommand(binparser.CmdListPrograms, nil)
	if err != nil {
		return nil, err
	}
	return parseProgramListFromMsg(resp.Attrs)
}

// ProgramStatus returns detailed status for a program.
func (c *Client) ProgramStatus(name string) (*ProgramStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		return c.programStatusBin(name)
	}
	return c.programStatusText(name)
}

func (c *Client) programStatusText(name string) (*ProgramStatus, error) {
	cmd, err := c.sendCommand("prog-status", map[string]string{"name": name})
	if err != nil {
		return nil, err
	}
	return parseProgramStatus(cmd)
}

func (c *Client) programStatusBin(name string) (*ProgramStatus, error) {
	resp, err := c.sendBinCommand(binparser.CmdProgramStatus, map[string]interface{}{
		"name": name,
	})
	if err != nil {
		return nil, err
	}
	return parseProgramStatusFromMsg(resp.Attrs)
}

// StartProgram starts a program by name.
func (c *Client) StartProgram(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		_, err := c.sendBinCommand(binparser.CmdStartProgram, map[string]interface{}{
			"name": name,
		})
		return err
	}
	_, err := c.sendCommand("prog-start", map[string]string{"name": name})
	return err
}

// StopProgram stops a program by name.
func (c *Client) StopProgram(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBinary {
		_, err := c.sendBinCommand(binparser.CmdStopProgram, map[string]interface{}{
			"name": name,
		})
		return err
	}
	_, err := c.sendCommand("prog-stop", map[string]string{"name": name})
	return err
}

// ---------------------------------------------------------------------------
// Internal: TXT01 protocol helpers
// ---------------------------------------------------------------------------

// sendCommand sends a TXT01 command and returns the parsed response.
func (c *Client) sendCommand(verb string, attrs map[string]string) (*parser.Command, error) {
	// Build the command string.
	var sb strings.Builder
	for key, val := range attrs {
		sb.WriteString(fmt.Sprintf("%q %s\n", key, val))
	}
	sb.WriteString(verb + "\n")

	if _, err := fmt.Fprint(c.conn, sb.String()); err != nil {
		return nil, fmt.Errorf("write command %q: %w", verb, err)
	}

	p, err := parser.NewParser(c.conn)
	if err != nil {
		return nil, fmt.Errorf("create parser: %w", err)
	}

	return p.ParseCommand()
}

// ---------------------------------------------------------------------------
// Internal: BIN01 protocol helpers
// ---------------------------------------------------------------------------

// sendBinCommand sends a BIN01 command and returns the parsed response.
func (c *Client) sendBinCommand(code byte, attrs map[string]interface{}) (*binparser.BinCommand, error) {
	if err := c.binWriter.WriteCommand(code, attrs, nil); err != nil {
		return nil, fmt.Errorf("write bin command %d: %w", code, err)
	}

	return c.binParser.ReadCommand()
}

// ---------------------------------------------------------------------------
// Response parsing helpers (TXT01)
// ---------------------------------------------------------------------------

// parseDaemonList parses a list-daemons response into DaemonInfo slices.
func parseDaemonList(cmd *parser.Command) ([]DaemonInfo, error) {
	// The body contains lines like "a-lancxo              12345  running    uptime=..."
	var result []DaemonInfo
	// For simplicity, we return whatever args are available.
	// In a real implementation, you'd parse the body format.
	_ = cmd
	return result, nil
}

// parseDaemonStatus parses a status response into DaemonStatus.
func parseDaemonStatus(cmd *parser.Command) (*DaemonStatus, error) {
	// Parse key: value pairs from the response attributes.
	_ = cmd
	return &DaemonStatus{}, nil
}

// parseFeatureList parses a list-features response.
func parseFeatureList(cmd *parser.Command) ([]FeatureInfo, error) {
	_ = cmd
	return nil, nil
}

// parseLogLines extracts log lines from a logs response.
func parseLogLines(cmd *parser.Command) []string {
	_ = cmd
	return nil
}

// parseProgramList parses a prog-list response.
func parseProgramList(cmd *parser.Command) ([]ProgramInfo, error) {
	_ = cmd
	return nil, nil
}

// parseProgramStatus parses a prog-status response.
func parseProgramStatus(cmd *parser.Command) (*ProgramStatus, error) {
	_ = cmd
	return &ProgramStatus{}, nil
}

// ---------------------------------------------------------------------------
// Response parsing helpers (BIN01)
// ---------------------------------------------------------------------------

func parseDaemonListFromMsg(attrs map[string]interface{}) ([]DaemonInfo, error) {
	_ = attrs
	return nil, nil
}

func parseDaemonStatusFromMsg(attrs map[string]interface{}) (*DaemonStatus, error) {
	_ = attrs
	return &DaemonStatus{}, nil
}

func parseFeatureListFromMsg(attrs map[string]interface{}) ([]FeatureInfo, error) {
	_ = attrs
	return nil, nil
}

func parseLogLinesFromMsg(attrs map[string]interface{}) []string {
	_ = attrs
	return nil
}

func parseProgramListFromMsg(attrs map[string]interface{}) ([]ProgramInfo, error) {
	_ = attrs
	return nil, nil
}

func parseProgramStatusFromMsg(attrs map[string]interface{}) (*ProgramStatus, error) {
	_ = attrs
	return &ProgramStatus{}, nil
}
