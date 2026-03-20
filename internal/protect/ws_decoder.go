package protect

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

// The UniFi Protect WebSocket update stream uses a custom binary protocol.
// Each message consists of two frames: an action frame and a payload frame.
//
// Frame header (8 bytes):
//   Byte 0:   Packet type  (1 = action, 2 = payload)
//   Byte 1:   Format       (1 = JSON, 2 = UTF-8 string, 3 = raw buffer)
//   Byte 2:   Deflated     (0 = uncompressed, 1 = zlib compressed)
//   Byte 3:   Reserved     (always 0)
//   Byte 4-7: Payload size (big-endian uint32)

const headerSize = 8

type frameType byte

const (
	actionFrame  frameType = 1
	payloadFrame frameType = 2
)

type payloadFormat byte

const (
	formatJSON   payloadFormat = 1
	formatString payloadFormat = 2
	formatBuffer payloadFormat = 3
)

// WSAction represents the action frame content of a websocket message.
type WSAction struct {
	Action      string `json:"action"`
	NewUpdateID string `json:"newUpdateId"`
	ModelKey    string `json:"modelKey"`
	ID          string `json:"id"`
}

// WSMessage represents a decoded websocket message with both frames.
type WSMessage struct {
	Action  WSAction
	Payload json.RawMessage
}

// DecodeWSMessage decodes a binary websocket message into its action and payload.
func DecodeWSMessage(data []byte) (*WSMessage, error) {
	if len(data) < headerSize*2 {
		return nil, errors.New("message too short")
	}

	firstPayloadSize := binary.BigEndian.Uint32(data[4:8])
	firstFrameEnd := headerSize + int(firstPayloadSize)

	if len(data) < firstFrameEnd+headerSize {
		return nil, errors.New("message too short for second header")
	}

	secondPayloadSize := binary.BigEndian.Uint32(data[firstFrameEnd+4 : firstFrameEnd+8])
	expectedLen := firstFrameEnd + headerSize + int(secondPayloadSize)

	if len(data) != expectedLen {
		return nil, errors.New("invalid packet size")
	}

	actionData, err := decodeFrame(data[:firstFrameEnd])
	if err != nil {
		return nil, err
	}

	payloadData, err := decodeFrame(data[firstFrameEnd:])
	if err != nil {
		return nil, err
	}

	var action WSAction
	if err := json.Unmarshal(actionData, &action); err != nil {
		return nil, err
	}

	return &WSMessage{
		Action:  action,
		Payload: json.RawMessage(payloadData),
	}, nil
}

func decodeFrame(data []byte) ([]byte, error) {
	if len(data) < headerSize {
		return nil, errors.New("frame too short")
	}

	deflated := data[2] == 1
	payloadSize := binary.BigEndian.Uint32(data[4:8])
	payload := data[headerSize : headerSize+int(payloadSize)]

	if deflated {
		r, err := zlib.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		defer r.Close()

		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			return nil, err
		}
		payload = buf.Bytes()
	}

	return payload, nil
}
