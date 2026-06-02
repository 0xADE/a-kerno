package binparser

import (
	"bytes"
	"io"
	"testing"
)

// encodeDecodeRoundtrip tests that encoding a value and then decoding it
// produces the same Go value.
func TestEncodeDecodeRoundtrip(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
	}{
		{"STR empty", ""},
		{"STR simple", "hello world"},
		{"STR unicode", "привет мир 🚀"},
		{"STR long", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"},
		{"INT zero", int64(0)},
		{"INT positive", int64(42)},
		{"INT negative", int64(-12345)},
		{"INT max", int64(9223372036854775807)},
		{"BOOL true", true},
		{"BOOL false", false},
		{"BLOB empty", []byte{}},
		{"BLOB simple", []byte{0x01, 0x02, 0x03, 0xFF}},
		{"BLOB null bytes", []byte{0x00, 0x00, 0x01, 0x00}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := encodeValue(tt.value)
			valType := encoded[0]
			data := encoded[1:]

			decoded, err := parseValue(valType, data)
			if err != nil {
				t.Fatalf("parseValue failed: %v", err)
			}

			// Compare.
			switch exp := tt.value.(type) {
			case string:
				got, ok := decoded.(string)
				if !ok {
					t.Fatalf("expected string, got %T", decoded)
				}
				if got != exp {
					t.Errorf("expected %q, got %q", exp, got)
				}
			case int64:
				got, ok := decoded.(int64)
				if !ok {
					t.Fatalf("expected int64, got %T", decoded)
				}
				if got != exp {
					t.Errorf("expected %d, got %d", exp, got)
				}
			case bool:
				got, ok := decoded.(bool)
				if !ok {
					t.Fatalf("expected bool, got %T", decoded)
				}
				if got != exp {
					t.Errorf("expected %v, got %v", exp, got)
				}
			case []byte:
				got, ok := decoded.([]byte)
				if !ok {
					t.Fatalf("expected []byte, got %T", decoded)
				}
				if !bytes.Equal(got, exp) {
					t.Errorf("expected %v, got %v", exp, got)
				}
			}
		})
	}
}

// TestCommandRoundtrip tests full command encoding → decoding via Writer → Parser.
func TestCommandRoundtrip(t *testing.T) {
	tests := []struct {
		name  string
		code  byte
		attrs map[string]interface{}
		data  []byte
	}{
		{
			name:  "list-daemons without attrs",
			code:  CmdListDaemons,
			attrs: nil,
			data:  nil,
		},
		{
			name: "status with name",
			code: CmdStatus,
			attrs: map[string]interface{}{
				"name": "a-lancxo",
			},
			data: nil,
		},
		{
			name: "logs with string and int attrs",
			code: CmdLogs,
			attrs: map[string]interface{}{
				"name":  "a-lancxo",
				"lines": int64(100),
			},
			data: nil,
		},
		{
			name: "start with multiple attrs",
			code: CmdStart,
			attrs: map[string]interface{}{
				"name":    "a-lancxo",
				"restart": true,
			},
			data: nil,
		},
		{
			name: "command with data",
			code: CmdShutdown,
			attrs: map[string]interface{}{
				"force": false,
			},
			data: []byte("shutdown reason: user request"),
		},
		{
			name: "prog-status with string name",
			code: CmdProgramStatus,
			attrs: map[string]interface{}{
				"name": "firefox",
			},
			data: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Write.
			writer := NewWriter(&buf)
			if err := writer.WriteCommand(tt.code, tt.attrs, tt.data); err != nil {
				t.Fatalf("WriteCommand failed: %v", err)
			}

			// Read back.
			reader := NewParser(&buf)
			cmd, err := reader.ReadCommand()
			if err != nil {
				t.Fatalf("ReadCommand failed: %v", err)
			}

			// Verify code.
			if cmd.Code != tt.code {
				t.Errorf("expected code %d, got %d", tt.code, cmd.Code)
			}

			// Verify attrs.
			if len(cmd.Attrs) != len(tt.attrs) {
				t.Errorf("expected %d attrs, got %d", len(tt.attrs), len(cmd.Attrs))
			}
			for key, expVal := range tt.attrs {
				gotVal, ok := cmd.Attrs[key]
				if !ok {
					t.Errorf("missing attr %q", key)
					continue
				}
				if !valuesEqual(expVal, gotVal) {
					t.Errorf("attr %q: expected %v (%T), got %v (%T)", key, expVal, expVal, gotVal, gotVal)
				}
			}

			// Verify data.
			if !bytes.Equal(cmd.Data, tt.data) {
				t.Errorf("expected data %v, got %v", tt.data, cmd.Data)
			}
		})
	}
}

// TestEndToEnd tests a full client→server interaction pattern.
func TestEndToEnd(t *testing.T) {
	// Simulate a pipe: two connected buffers.
	var clientBuf, serverBuf bytes.Buffer

	// Client: write a command.
	clientWriter := NewWriter(&clientBuf)
	err := clientWriter.WriteCommand(CmdStatus, map[string]interface{}{
		"name": "a-lancxo",
	}, nil)
	if err != nil {
		t.Fatalf("client WriteCommand: %v", err)
	}
	// Copy client output to server input.
	serverBuf.Write(clientBuf.Bytes())
	clientBuf.Reset()

	// Server: read the command.
	serverParser := NewParser(&serverBuf)
	cmd, err := serverParser.ReadCommand()
	if err != nil {
		t.Fatalf("server ReadCommand: %v", err)
	}
	if cmd.Code != CmdStatus {
		t.Errorf("expected code %d, got %d", CmdStatus, cmd.Code)
	}
	if name, ok := cmd.Attrs["name"]; !ok || name != "a-lancxo" {
		t.Errorf("expected attr name=a-lancxo, got %v", cmd.Attrs)
	}

	// Server: write response.
	serverWriter := NewWriter(&serverBuf)
	serverBuf.Reset()
	err = serverWriter.WriteResponse(CodeOK, "daemon a-lancxo is running")
	if err != nil {
		t.Fatalf("server WriteResponse: %v", err)
	}

	// Copy server output to client input.
	clientBuf.Write(serverBuf.Bytes())

	// Client: read response.
	clientParser := NewParser(&clientBuf)
	resp, err := clientParser.ReadCommand()
	if err != nil {
		t.Fatalf("client ReadCommand: %v", err)
	}
	if resp.Code != CodeOK {
		t.Errorf("expected response code %d, got %d", CodeOK, resp.Code)
	}
	msg, ok := resp.Attrs["message"].(string)
	if !ok {
		t.Fatalf("expected message attr as string, got %T", resp.Attrs["message"])
	}
	if msg != "daemon a-lancxo is running" {
		t.Errorf("unexpected message: %q", msg)
	}

	// Server: write error.
	serverBuf.Reset()
	err = serverWriter.WriteError("daemon not found")
	if err != nil {
		t.Fatalf("server WriteError: %v", err)
	}
	clientBuf.Reset()
	clientBuf.Write(serverBuf.Bytes())
	resp, err = NewParser(&clientBuf).ReadCommand()
	if err != nil {
		t.Fatalf("client ReadCommand (error): %v", err)
	}
	if resp.Code != CodeError {
		t.Errorf("expected error code %d, got %d", CodeError, resp.Code)
	}
}

// TestParserErrorIncompleteFrame tests error handling for incomplete frames.
func TestParserErrorIncompleteFrame(t *testing.T) {
	// Only 3 bytes of header — not enough.
	buf := bytes.NewBuffer([]byte{0x01, 0x02, 0x03})
	parser := NewParser(buf)
	_, err := parser.ReadCommand()
	if err == nil {
		t.Error("expected error for incomplete frame header")
	}
}

// TestParserErrorUnknownFrameType tests handling of unknown frame types.
func TestParserErrorUnknownFrameType(t *testing.T) {
	// Frame type 0xFF is unknown.
	frame := []byte{
		0xFF,                   // unknown type
		0x00, 0x00, 0x00, 0x00, // length = 0
	}
	buf := bytes.NewBuffer(frame)
	parser := NewParser(buf)
	_, err := parser.ReadCommand()
	if err == nil {
		t.Error("expected error for unknown frame type")
	}
}

// TestParserErrorEndWithoutCmd tests END frame without preceding CMD.
func TestParserErrorEndWithoutCmd(t *testing.T) {
	frame := []byte{
		FrameEND,
		0x00, 0x00, 0x00, 0x00, // length = 0
	}
	buf := bytes.NewBuffer(frame)
	parser := NewParser(buf)
	_, err := parser.ReadCommand()
	if err == nil {
		t.Error("expected error for END without CMD")
	}
}

// TestParserErrorAttrTooShort tests an ATTR frame with insufficient payload.
func TestParserErrorAttrTooShort(t *testing.T) {
	// CMD frame first (valid).
	// Then ATTR with too-short payload.
	data := []byte{
		// CMD frame: type=CMD, length=1, code=Status.
		FrameCMD, 0x01, 0x00, 0x00, 0x00, CmdStatus,
		// ATTR frame: type=ATTR, length=0 (no payload) — too short.
		FrameATTR, 0x00, 0x00, 0x00, 0x00,
		// END frame.
		FrameEND, 0x00, 0x00, 0x00, 0x00,
	}
	buf := bytes.NewBuffer(data)
	parser := NewParser(buf)
	_, err := parser.ReadCommand()
	if err == nil {
		t.Error("expected error for ATTR frame with no payload")
	}
}

// TestResponseRoundtrip tests WriteResponse and WriteError.
func TestResponseRoundtrip(t *testing.T) {
	tests := []struct {
		name     string
		respFn   func(w *Writer) error
		wantMsg  string
		wantCode byte
	}{
		{
			name: "OK response",
			respFn: func(w *Writer) error {
				return w.WriteResponse(CodeOK, "all good")
			},
			wantMsg:  "all good",
			wantCode: CodeOK,
		},
		{
			name: "Error response",
			respFn: func(w *Writer) error {
				return w.WriteError("something went wrong")
			},
			wantMsg:  "something went wrong",
			wantCode: CodeError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			if err := tt.respFn(w); err != nil {
				t.Fatalf("write failed: %v", err)
			}

			parser := NewParser(&buf)
			cmd, err := parser.ReadCommand()
			if err != nil {
				t.Fatalf("read failed: %v", err)
			}
			if cmd.Code != tt.wantCode {
				t.Errorf("expected code %d, got %d", tt.wantCode, cmd.Code)
			}
			msg, ok := cmd.Attrs["message"].(string)
			if !ok {
				t.Fatalf("expected string message, got %T", cmd.Attrs["message"])
			}
			if msg != tt.wantMsg {
				t.Errorf("expected message %q, got %q", tt.wantMsg, msg)
			}
		})
	}
}

// TestMultipleCommands tests reading multiple commands from a single stream.
func TestMultipleCommands(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	// Write two commands.
	if err := w.WriteCommand(CmdListDaemons, nil, nil); err != nil {
		t.Fatalf("write cmd1: %v", err)
	}
	if err := w.WriteCommand(CmdListFeatures, nil, nil); err != nil {
		t.Fatalf("write cmd2: %v", err)
	}

	parser := NewParser(&buf)

	cmd1, err := parser.ReadCommand()
	if err != nil {
		t.Fatalf("read cmd1: %v", err)
	}
	if cmd1.Code != CmdListDaemons {
		t.Errorf("cmd1: expected %d, got %d", CmdListDaemons, cmd1.Code)
	}

	cmd2, err := parser.ReadCommand()
	if err != nil {
		t.Fatalf("read cmd2: %v", err)
	}
	if cmd2.Code != CmdListFeatures {
		t.Errorf("cmd2: expected %d, got %d", CmdListFeatures, cmd2.Code)
	}

	// EOF after last command.
	_, err = parser.ReadCommand()
	if err != io.EOF {
		t.Errorf("expected EOF after last command, got %v", err)
	}
}

// valuesEqual compares two values for equality, handling the types used in BIN01.
func valuesEqual(a, b interface{}) bool {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case int64:
		bv, ok := b.(int64)
		return ok && av == bv
	case int:
		switch bv := b.(type) {
		case int64:
			return int64(av) == bv
		case int:
			return av == bv
		}
		return false
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case []byte:
		bv, ok := b.([]byte)
		return ok && bytes.Equal(av, bv)
	}
	return false
}
