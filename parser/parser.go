// Package parser implements the CMDLIST protocol parser for a-kerno.
// It supports the TXT01 text format for command serialization over
// Unix sockets, following the same protocol as a-lancxo.
package parser

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ValueType represents the type of a value on the stack.
type ValueType int

const (
	TypeString ValueType = iota
	TypeInt
	TypeBool
)

// Value represents a value on the stack.
type Value struct {
	Type ValueType
	Str  string
	Int  int64
	Bool bool
}

// Command represents a parsed command with its name and arguments.
type Command struct {
	Name string
	Args []Value
}

// Response codes for CMDLIST protocol.
const (
	CodeOK    = 20
	CodeError = 50
)

// Parser parses CMDLIST TXT01 commands from an io.Reader.
type Parser struct {
	reader  *bufio.Reader
	header  string
	version string
}

// NewParser creates a new CMDLIST parser from the given reader.
// It reads and validates the TXT01 header.
func NewParser(reader io.Reader) (*Parser, error) {
	p := &Parser{
		reader: bufio.NewReader(reader),
	}

	// Read header: 5 bytes — "TXT" + 2-digit version.
	headerBytes := make([]byte, 5)
	n, err := io.ReadFull(p.reader, headerBytes)
	if err != nil || n != 5 {
		return nil, fmt.Errorf("invalid header: expected 5 bytes, got %d", n)
	}

	p.header = string(headerBytes[:3])
	p.version = string(headerBytes[3:5])

	if p.header != "TXT" {
		return nil, fmt.Errorf("unsupported format: %s (expected TXT)", p.header)
	}

	return p, nil
}

// ParseCommand parses the next command from the input.
// It returns io.EOF when no more commands are available.
func (p *Parser) ParseCommand() (*Command, error) {
	stack := make([]Value, 0)

	for {
		line, err := p.reader.ReadString('\n')
		if err == io.EOF {
			if len(stack) == 0 {
				return nil, io.EOF
			}
			// Return the last command if the stack is not empty.
			break
		}
		if err != nil {
			return nil, err
		}

		line = strings.TrimSpace(line)

		// Skip empty lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check if the line is a known command verb.
		if cmd := resolveCommand(line); cmd != "" {
			return &Command{
				Name: cmd,
				Args: stack,
			}, nil
		}

		// Otherwise, parse as a value and push onto the stack.
		value, err := parseValue(line)
		if err != nil {
			return nil, fmt.Errorf("parse error: %w", err)
		}
		stack = append(stack, value)
	}

	return nil, io.EOF
}

// ReadAllCommands reads all remaining commands from the parser.
func (p *Parser) ReadAllCommands() ([]*Command, error) {
	var commands []*Command

	for {
		cmd, err := p.ParseCommand()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}

	return commands, nil
}

// Header returns the parsed header string (e.g. "TXT").
func (p *Parser) Header() string {
	return p.header
}

// Version returns the parsed protocol version (e.g. "01").
func (p *Parser) Version() string {
	return p.version
}

// resolveCommand checks whether the given line is a known command verb
// and returns its canonical name, or "" if it is not a command.
func resolveCommand(line string) string {
	commands := map[string]bool{
		// Daemon lifecycle commands.
		"start":   true,
		"stop":    true,
		"restart": true,
		"status":  true,

		// a-kerno management commands.
		"list-daemons":  true,
		"list-features": true,
		"logs":          true,
		"shutdown":      true,

		// Query commands.
		"list":   true,
		"reload": true,

		// Program management commands.
		"prog-start":  true,
		"prog-stop":   true,
		"prog-status": true,
		"prog-list":   true,

		// Generic CMDLIST commands.
		"help":    true,
		"version": true,
		"quit":    true,
	}

	if commands[line] {
		return line
	}
	return ""
}

// parseValue parses a single value line from the CMDLIST stack.
//
// Value formats:
//   - "string value" — string (prefixed with ")
//   - 123           — integer
//   - t / f         — boolean
func parseValue(line string) (Value, error) {
	// String value: starts with '"'.
	if after, ok := strings.CutPrefix(line, `"`); ok {
		return Value{Type: TypeString, Str: after}, nil
	}

	// Boolean literals.
	switch line {
	case "t":
		return Value{Type: TypeBool, Bool: true}, nil
	case "f":
		return Value{Type: TypeBool, Bool: false}, nil
	}

	// Integer: must be all digits (optionally with leading minus).
	if intVal, err := strconv.ParseInt(line, 10, 64); err == nil {
		return Value{Type: TypeInt, Int: intVal}, nil
	}

	return Value{}, fmt.Errorf("cannot parse value: %s", line)
}

// FormatResponse formats a CMDLIST response with the given code and message.
// The response follows the TXT01 format: the code on its own line,
// followed by the message string.
func FormatResponse(code int, message string) string {
	return fmt.Sprintf("%d\n\"%s\n", code, message)
}

// FormatOK returns a standard OK (20) response with the given message.
func FormatOK(message string) string {
	return FormatResponse(CodeOK, message)
}

// FormatOKWithBody returns a standard OK (20) response with an attributes
// block and optional body content. The response ends with "\n\n" (two
// consecutive newlines) which signals the end of the CMDLIST response.
//
// Example:
//
//	FormatOKWithBody("name: a-lancxo\nstatus: ready", "pid=12345")
//	→ "20 OK\nname: a-lancxo\nstatus: ready\n\nbody:\npid=12345\n\n"
func FormatOKWithBody(attrs string, body string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d OK\n", CodeOK))
	if attrs != "" {
		sb.WriteString(attrs)
		if !strings.HasSuffix(attrs, "\n") {
			sb.WriteByte('\n')
		}
	}
	sb.WriteByte('\n')
	if body != "" {
		sb.WriteString("body:\n")
		sb.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			sb.WriteByte('\n')
		}
	}
	sb.WriteByte('\n')
	return sb.String()
}

// FormatError returns a standard ERROR (50) response with the given message.
func FormatError(message string) string {
	return FormatResponse(CodeError, message)
}
