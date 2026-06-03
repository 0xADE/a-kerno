package binparser

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// Writer writes binary frames to an io.Writer.
type Writer struct {
	writer *bufio.Writer
}

// NewWriter creates a new BIN01 writer writing to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{
		writer: bufio.NewWriter(w),
	}
}

// WriteCommand writes a command with attributes and optional data.
// The command is written as: CMD frame, ATTR frames (one per attribute),
// optional DATA frames, END frame.
func (w *Writer) WriteCommand(code byte, attrs map[string]interface{}, data []byte) error {
	// CMD frame.
	if err := w.writeFrame(FrameCMD, []byte{code}); err != nil {
		return fmt.Errorf("write CMD frame: %w", err)
	}

	// ATTR frames.
	for key, val := range attrs {
		attrPayload := encodeAttr(key, val)
		if err := w.writeFrame(FrameATTR, attrPayload); err != nil {
			return fmt.Errorf("write ATTR frame %q: %w", key, err)
		}
	}

	// DATA frames (split into chunks if needed, though typically one frame).
	if len(data) > 0 {
		if err := w.writeFrame(FrameDATA, data); err != nil {
			return fmt.Errorf("write DATA frame: %w", err)
		}
	}

	// END frame.
	if err := w.writeFrame(FrameEND, nil); err != nil {
		return fmt.Errorf("write END frame: %w", err)
	}

	return w.Flush()
}

// WriteResponse writes a response: response code (1B) + text message.
// This is a convenience wrapper: it sends a CMD frame with the response code,
// a single ATTR "message" (string), and END.
func (w *Writer) WriteResponse(respCode byte, message string) error {
	attrs := map[string]interface{}{
		"message": message,
	}
	return w.WriteCommand(respCode, attrs, nil)
}

// WriteError writes an error response (code 50) with the given message.
func (w *Writer) WriteError(message string) error {
	return w.WriteResponse(CodeError, message)
}

// Flush flushes any buffered data to the underlying writer.
func (w *Writer) Flush() error {
	return w.writer.Flush()
}

// writeFrame writes a single frame: type (1B) + length (4B LE) + payload.
func (w *Writer) writeFrame(frameType byte, payload []byte) error {
	header := make([]byte, 5)
	header[0] = frameType
	binary.LittleEndian.PutUint32(header[1:5], uint32(len(payload))) //nolint:gosec // length bounded by frame protocol

	if _, err := w.writer.Write(header); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.writer.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// encodeAttr encodes a key-value pair into an ATTR payload:
//
//	key_len (2B BE) + key (bytes) + value_type (1B) + encoded_value
func encodeAttr(key string, val interface{}) []byte {
	keyBytes := []byte(key)
	valBytes := encodeValue(val)

	buf := make([]byte, 0, 2+len(keyBytes)+len(valBytes))

	// Key length (2 bytes, big-endian).
	keyLen := uint16(len(keyBytes)) //nolint:gosec // key length bounded
	buf = append(buf, byte(keyLen>>8), byte(keyLen&0xFF))

	// Key.
	buf = append(buf, keyBytes...)

	// Encoded value.
	buf = append(buf, valBytes...)

	return buf
}

// encodeValue encodes a Go value into a BIN01 value:
//
//	1B type tag + encoded data
func encodeValue(val interface{}) []byte {
	switch v := val.(type) {
	case string:
		return encodeSTR(v)
	case int64:
		return encodeINT(v)
	case int:
		return encodeINT(int64(v))
	case bool:
		return encodeBOOL(v)
	case []byte:
		return encodeBLOB(v)
	default:
		// Fallback: encode as string representation.
		return encodeSTR(fmt.Sprintf("%v", v))
	}
}

// encodeSTR encodes a string: type (0x10) + length (4B LE) + UTF-8 bytes.
func encodeSTR(s string) []byte {
	b := make([]byte, 1+4+len(s))
	b[0] = ValSTR
	binary.LittleEndian.PutUint32(b[1:5], uint32(len(s))) //nolint:gosec // string length bounded
	copy(b[5:], s)
	return b
}

// encodeINT encodes an int64: type (0x11) + 8 bytes LE.
func encodeINT(v int64) []byte {
	b := make([]byte, 1+8)
	b[0] = ValINT
	binary.LittleEndian.PutUint64(b[1:9], uint64(v)) //nolint:gosec // BIN01 protocol uses uint64 for int64
	return b
}

// encodeBOOL encodes a bool: type (0x12) + 1 byte.
func encodeBOOL(v bool) []byte {
	b := make([]byte, 2)
	b[0] = ValBOOL
	if v {
		b[1] = 1
	}
	return b
}

// encodeBLOB encodes a byte slice: type (0x13) + length (4B LE) + bytes.
func encodeBLOB(v []byte) []byte {
	b := make([]byte, 1+4+len(v))
	b[0] = ValBLOB
	binary.LittleEndian.PutUint32(b[1:5], uint32(len(v))) //nolint:gosec // blob length bounded
	copy(b[5:], v)
	return b
}
