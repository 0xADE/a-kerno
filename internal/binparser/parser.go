// Package binparser implements the BIN01 binary protocol for a-kerno.
// BIN01 is a binary alternative to the text-based CMDLIST TXT01 protocol.
// It uses fixed-width fields and command codes instead of strings.
//
// Frame format:
//
//	| type (1B) | length (4B LE) | payload (length bytes) |
//
// Value types:
//
//	STR  (0x10) — UTF-8 string, length 4B LE + bytes
//	INT  (0x11) — signed 64-bit integer, little-endian
//	BOOL (0x12) — single byte (0 or 1)
//	BLOB (0x13) — binary data, length 4B LE + bytes
package binparser

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// Frame types.
const (
	FrameCMD  byte = 0x01
	FrameATTR byte = 0x02
	FrameDATA byte = 0x03
	FrameEND  byte = 0x04
)

// Value types.
const (
	ValSTR  byte = 0x10
	ValINT  byte = 0x11
	ValBOOL byte = 0x12
	ValBLOB byte = 0x13
)

// Command codes.
const (
	CmdListDaemons   byte = 1
	CmdStatus        byte = 2
	CmdRestart       byte = 3
	CmdStop          byte = 4
	CmdStart         byte = 5
	CmdListFeatures  byte = 6
	CmdLogs          byte = 7
	CmdShutdown      byte = 8
	CmdListPrograms  byte = 9
	CmdProgramStatus byte = 10
	CmdStartProgram  byte = 11
	CmdStopProgram   byte = 12
)

// Response codes (same as TXT01).
const (
	CodeOK    byte = 20
	CodeError byte = 50
)

// BinCommand represents a parsed binary command.
type BinCommand struct {
	Code  byte
	Attrs map[string]interface{} // string | int64 | bool | []byte
	Data  []byte
}

// Parser reads binary frames from an io.Reader.
type Parser struct {
	reader *bufio.Reader
}

// NewParser creates a new BIN01 parser reading from r.
func NewParser(r io.Reader) *Parser {
	return &Parser{
		reader: bufio.NewReader(r),
	}
}

// ReadCommand reads frames until FrameEND, collecting attributes and data
// into a BinCommand. Returns io.EOF if the stream ends before a complete
// command is formed.
func (p *Parser) ReadCommand() (*BinCommand, error) {
	var (
		cmd   *BinCommand
		attrs map[string]interface{}
		data  []byte
	)

	for {
		frameType, payload, err := p.readFrame()
		if err != nil {
			return nil, err
		}

		switch frameType {
		case FrameCMD:
			if len(payload) < 1 {
				return nil, fmt.Errorf("CMD frame too short: %d bytes", len(payload))
			}
			cmd = &BinCommand{
				Code:  payload[0],
				Attrs: nil,
				Data:  nil,
			}
		case FrameATTR:
			if attrs == nil {
				attrs = make(map[string]interface{})
			}
			key, val, err := parseAttr(payload)
			if err != nil {
				return nil, fmt.Errorf("ATTR frame: %w", err)
			}
			attrs[key] = val
		case FrameDATA:
			data = append(data, payload...)
		case FrameEND:
			if cmd == nil {
				return nil, fmt.Errorf("END frame without preceding CMD frame")
			}
			cmd.Attrs = attrs
			cmd.Data = data
			return cmd, nil
		default:
			return nil, fmt.Errorf("unknown frame type: 0x%02x", frameType)
		}
	}
}

// readFrame reads a single frame: type (1B) + length (4B LE) + payload.
func (p *Parser) readFrame() (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(p.reader, header); err != nil {
		return 0, nil, err
	}

	frameType := header[0]
	length := binary.LittleEndian.Uint32(header[1:5])

	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(p.reader, payload); err != nil {
			return 0, nil, fmt.Errorf("read payload (%d bytes): %w", length, err)
		}
	}

	return frameType, payload, nil
}

// parseAttr parses an ATTR payload: key_len (2B BE) + key + value.
// The value starts with a 1-byte type tag.
func parseAttr(payload []byte) (string, interface{}, error) {
	if len(payload) < 3 {
		return "", nil, fmt.Errorf("ATTR payload too short: %d bytes", len(payload))
	}

	keyLen := uint16(payload[0])<<8 | uint16(payload[1])
	if int(keyLen)+2 > len(payload) {
		return "", nil, fmt.Errorf("key length %d exceeds payload size %d", keyLen, len(payload)-2)
	}

	key := string(payload[2 : 2+keyLen])
	valPayload := payload[2+keyLen:]

	if len(valPayload) < 1 {
		return "", nil, fmt.Errorf("no value type tag in ATTR payload")
	}

	valType := valPayload[0]
	valData := valPayload[1:]

	val, err := parseValue(valType, valData)
	if err != nil {
		return "", nil, fmt.Errorf("attr %q: %w", key, err)
	}

	return key, val, nil
}

// parseValue parses a single value from its type tag and data.
func parseValue(valType byte, data []byte) (interface{}, error) {
	switch valType {
	case ValSTR:
		if len(data) < 4 {
			return nil, fmt.Errorf("STR value too short: %d bytes", len(data))
		}
		strLen := binary.LittleEndian.Uint32(data[:4])
		if int(strLen)+4 > len(data) {
			return nil, fmt.Errorf("STR length %d exceeds data size %d", strLen, len(data)-4)
		}
		return string(data[4 : 4+strLen]), nil

	case ValINT:
		if len(data) < 8 {
			return nil, fmt.Errorf("INT value too short: %d bytes", len(data))
		}
		return int64(binary.LittleEndian.Uint64(data[:8])), nil

	case ValBOOL:
		if len(data) < 1 {
			return nil, fmt.Errorf("BOOL value too short: %d bytes", len(data))
		}
		return data[0] != 0, nil

	case ValBLOB:
		if len(data) < 4 {
			return nil, fmt.Errorf("BLOB value too short: %d bytes", len(data))
		}
		blobLen := binary.LittleEndian.Uint32(data[:4])
		if int(blobLen)+4 > len(data) {
			return nil, fmt.Errorf("BLOB length %d exceeds data size %d", blobLen, len(data)-4)
		}
		out := make([]byte, blobLen)
		copy(out, data[4:4+blobLen])
		return out, nil

	default:
		return nil, fmt.Errorf("unknown value type: 0x%02x", valType)
	}
}

// readUint32LE reads a 4-byte little-endian uint32 from the reader.
func readUint32LE(r io.Reader) (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf[:]), nil
}

// readUint16BE reads a 2-byte big-endian uint16 from the reader.
func readUint16BE(r io.Reader) (uint16, error) {
	var buf [2]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return uint16(buf[0])<<8 | uint16(buf[1]), nil
}

// readInt64LE reads an 8-byte little-endian int64 from the reader.
func readInt64LE(r io.Reader) (int64, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(buf[:])), nil
}
