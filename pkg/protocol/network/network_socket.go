package network

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const maxSocketReadBytes = 1 << 20

func (n *Network) runSocket(ctx context.Context, action *protocol.Action, networkName string) (*operationResult, error) {
	readLimit := actionParamInt(action.Params, "read_bytes")
	if readLimit > maxSocketReadBytes {
		return nil, fmt.Errorf("read_bytes exceeds maximum of %d", maxSocketReadBytes)
	}
	target, err := n.endpoint(action, defaultPortForSubprotocol(networkName))
	if err != nil {
		return nil, err
	}
	conn, connectTime, err := n.dial(ctx, networkName, target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	payload := socketPayload(action)
	var bytesSent int64
	if len(payload) > 0 {
		written, err := conn.Write(payload)
		bytesSent = int64(written)
		if err != nil {
			return nil, err
		}
	}
	readRequested := actionParamBool(action.Params, "read") || readLimit > 0
	if readLimit <= 0 {
		readLimit = 4096
	}
	var response []byte
	if readRequested {
		response = make([]byte, readLimit)
		read, err := conn.Read(response)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		response = response[:read]
	}
	body := encodePayload(map[string]any{
		"network":        networkName,
		"target":         target,
		"bytes_sent":     bytesSent,
		"bytes_received": len(response),
		"response":       string(response),
	})
	return &operationResult{
		payload:       body,
		target:        target,
		dependency:    "network_" + networkName,
		bytesSent:     bytesSent,
		bytesReceived: int64(len(response)),
		connectTime:   connectTime,
		messagesSent:  boolCount(len(payload) > 0),
		messagesRecv:  boolCount(len(response) > 0),
	}, nil
}
