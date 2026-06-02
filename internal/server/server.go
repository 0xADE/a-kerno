// Package server implements the management Unix socket server for a-kerno.
// It listens on the Kerno socket path, accepts connections, parses CMDLIST
// commands (TXT01 text or BIN01 binary), dispatches them to handlers,
// and writes responses back.
package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/0xADE/a-kerno/internal/binparser"
	"github.com/0xADE/a-kerno/internal/config"
	"github.com/0xADE/a-kerno/internal/daemon"
	"github.com/0xADE/a-kerno/internal/feature"
	"github.com/0xADE/a-kerno/internal/program"
	"github.com/0xADE/a-kerno/parser"
)

// Server is the management Unix socket server for a-kerno.
// It accepts CMDLIST connections on the Kerno socket path,
// dispatches commands to handlers, and returns responses.
type Server struct {
	socketPath string
	listener   net.Listener
	manager    *daemon.DaemonManager
	pm         *program.ProgramManager
	features   *feature.Registry
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	logger     *slog.Logger
}

// NewServer creates a new Server with the given dependencies.
func NewServer(cfg *config.Config, mgr *daemon.DaemonManager, feats *feature.Registry, pm *program.ProgramManager) *Server {
	return &Server{
		socketPath: cfg.KernoSock,
		manager:    mgr,
		pm:         pm,
		features:   feats,
		logger:     slog.Default().With("component", "server"),
	}
}

// Start creates the Unix socket directory, removes any stale socket file,
// listens on the socket path, and starts the accept loop in a goroutine.
func (s *Server) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Create socket directory with restricted permissions.
	socketDir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(socketDir, 0750); err != nil {
		return fmt.Errorf("create socket directory %s: %w", socketDir, err)
	}

	// Remove stale socket file if it exists.
	_ = os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.socketPath, err)
	}
	s.listener = listener

	s.logger.Info("management socket listening", "path", s.socketPath)

	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

// Stop closes the listener, waits for all connection handlers to finish,
// and removes the socket file.
func (s *Server) Stop() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.wg.Wait()

	if s.socketPath != "" {
		_ = os.Remove(s.socketPath)
		s.logger.Info("removed socket file", "path", s.socketPath)
	}
}

// ShutdownC returns a channel that is closed when a shutdown command
// is received via the management socket.
func (s *Server) ShutdownC() <-chan struct{} {
	if s.ctx != nil {
		return s.ctx.Done()
	}
	ch := make(chan struct{})
	close(ch)
	return ch
}

// acceptLoop accepts connections on the listener and dispatches each
// connection to handleConnection in a separate goroutine.
func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			// Graceful shutdown: net.ErrClosed is expected when listener is closed.
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Check if context is done for graceful exit.
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			s.logger.Warn("accept error", "error", err)
			continue
		}

		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

// handleConnection reads CMDLIST commands from a single connection,
// dispatches each command, writes the response, and closes the connection.
//
// Protocol auto-detection:
//   - Client sends 4-byte magic "BIN1" → binary BIN01 mode
//   - Otherwise → text TXT01 mode (reads the magic + rest of the line as command)
func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	// Read first 4 bytes to detect binary protocol.
	magic := make([]byte, 4)
	if _, err := io.ReadFull(conn, magic); err != nil {
		s.logger.Warn("failed to read magic", "error", err)
		return
	}

	if string(magic) == "BIN1" {
		// Acknowledge binary mode.
		if _, err := conn.Write([]byte("BIN1")); err != nil {
			s.logger.Warn("failed to ack BIN1", "error", err)
			return
		}
		s.handleBinConnection(conn)
		return
	}

	// Text mode: prepend the magic bytes back for the TXT01 parser.
	// The TXT01 parser expects "TXT01" as the first 5 bytes.
	// We already consumed 4 bytes; we need to read 1 more byte to complete the header.
	// But the client may have sent "TXT01" — we already have "TXT0",
	// so read 1 more byte, then feed all 5 to the parser.
	fifthByte := make([]byte, 1)
	if _, err := io.ReadFull(conn, fifthByte); err != nil {
		s.logger.Warn("failed to read TXT01 5th byte", "error", err)
		s.writeError(conn, "", "invalid header", err.Error())
		return
	}

	headerBytes := append(magic, fifthByte...)

	// Wrap remaining connection with the header bytes prepended.
	r := io.MultiReader(
		bytesReader(headerBytes),
		conn,
	)

	p, err := parser.NewParser(r)
	if err != nil {
		s.logger.Warn("failed to create parser", "error", err)
		s.writeError(conn, "", "invalid header", err.Error())
		return
	}

	for {
		cmd, err := p.ParseCommand()
		if err == io.EOF {
			return
		}
		if err != nil {
			s.logger.Warn("parse error", "error", err)
			s.writeError(conn, "", "parse error", err.Error())
			return
		}

		response := s.dispatch(cmd)
		if _, writeErr := fmt.Fprint(conn, response); writeErr != nil {
			s.logger.Warn("write response error", "error", writeErr)
			return
		}

		// shutdown terminates the accept loop; close connection.
		if cmd.Name == "shutdown" {
			return
		}
	}
}

// handleBinConnection handles a connection using the BIN01 binary protocol.
func (s *Server) handleBinConnection(conn net.Conn) {
	binParser := binparser.NewParser(conn)
	binWriter := binparser.NewWriter(conn)

	for {
		cmd, err := binParser.ReadCommand()
		if err == io.EOF {
			return
		}
		if err != nil {
			s.logger.Warn("bin parse error", "error", err)
			binWriter.WriteError(err.Error())
			return
		}

		responseCode, responseMsg := s.dispatchBinCommand(cmd)
		if cmd.Code == binparser.CmdShutdown {
			// Send OK before closing.
			binWriter.WriteResponse(responseCode, responseMsg)
			return
		}
		if err := binWriter.WriteResponse(responseCode, responseMsg); err != nil {
			s.logger.Warn("bin write response error", "error", err)
			return
		}
	}
}

// dispatchBinCommand routes a parsed binary command to the appropriate handler.
func (s *Server) dispatchBinCommand(cmd *binparser.BinCommand) (byte, string) {
	switch cmd.Code {
	case binparser.CmdListDaemons:
		return handleBinListDaemons(s, cmd)
	case binparser.CmdStatus:
		return handleBinStatus(s, cmd)
	case binparser.CmdRestart:
		return handleBinRestart(s, cmd)
	case binparser.CmdStop:
		return handleBinStop(s, cmd)
	case binparser.CmdStart:
		return handleBinStart(s, cmd)
	case binparser.CmdListFeatures:
		return handleBinListFeatures(s, cmd)
	case binparser.CmdLogs:
		return handleBinLogs(s, cmd)
	case binparser.CmdShutdown:
		return handleBinShutdown(s, cmd)
	case binparser.CmdListPrograms:
		return handleBinListPrograms(s, cmd)
	case binparser.CmdProgramStatus:
		return handleBinProgramStatus(s, cmd)
	case binparser.CmdStartProgram:
		return handleBinStartProgram(s, cmd)
	case binparser.CmdStopProgram:
		return handleBinStopProgram(s, cmd)
	default:
		return binparser.CodeError, fmt.Sprintf("unknown binary command code: %d", cmd.Code)
	}
}

// dispatch routes a parsed command to the appropriate handler.
func (s *Server) dispatch(cmd *parser.Command) string {
	switch cmd.Name {
	case "list-daemons":
		return handleListDaemons(s, cmd)
	case "status":
		return handleStatus(s, cmd)
	case "restart":
		return handleRestart(s, cmd)
	case "stop":
		return handleStop(s, cmd)
	case "start":
		return handleStart(s, cmd)
	case "list-features":
		return handleListFeatures(s, cmd)
	case "logs":
		return handleLogs(s, cmd)
	case "shutdown":
		return handleShutdown(s, cmd)
	case "prog-list":
		return handleListPrograms(s, cmd)
	case "prog-status":
		return handleProgramStatus(s, cmd)
	case "prog-start":
		return handleStartProgram(s, cmd)
	case "prog-stop":
		return handleStopProgram(s, cmd)
	default:
		return parser.FormatError(fmt.Sprintf("unknown command: %s", cmd.Name))
	}
}

// writeError sends a CMDLIST error response to the connection.
func (s *Server) writeError(conn net.Conn, cmdName, errType, desc string) {
	var msg string
	if cmdName != "" {
		msg = fmt.Sprintf("error-cmd: %s\nerror: %s\ndesc: %s\n\n", cmdName, errType, desc)
	} else {
		msg = fmt.Sprintf("error: %s\ndesc: %s\n\n", errType, desc)
	}
	// Best-effort write; connection may already be broken.
	_, _ = fmt.Fprint(conn, msg)
}

// readLine reads a single line from the connection using a bufio.Scanner.
// This is a utility function shared by handlers that need to read extra
// data beyond the CMDLIST command.
func readLine(conn net.Conn) (string, error) {
	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", io.EOF
}

// bytesReader returns an io.Reader that yields the given bytes.
// It is used to prepend already-read header bytes back into a connection stream.
type bytesReader []byte

func (b bytesReader) Read(p []byte) (int, error) {
	if len(b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b)
	return n, nil
}
