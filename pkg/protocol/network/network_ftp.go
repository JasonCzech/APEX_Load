package network

import (
	"context"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func (n *Network) runFTP(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	target, err := n.endpoint(action, "21")
	if err != nil {
		return nil, err
	}
	conn, connectTime, err := n.dial(ctx, "tcp", target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tp := newBoundedTextProtoConn(conn)
	defer tp.Close()

	lines := []string{}
	greeting, err := readResponseLine(tp)
	if err != nil {
		return nil, err
	}
	lines = append(lines, greeting)
	username := protocol.FirstNonEmpty(actionParamString(action.Params, "username"), n.username, "anonymous")
	password := protocol.FirstNonEmpty(actionParamString(action.Params, "password"), n.password, "apexload@example.invalid")
	if username != "" {
		lines = append(lines, n.commandResponse(tp, "USER "+username))
		if password != "" {
			lines = append(lines, n.commandResponse(tp, "PASS "+password))
		}
	}
	command := ftpCommand(action)
	if command == "" {
		command = "NOOP"
	}
	lines = append(lines, n.commandResponse(tp, command))
	lines = append(lines, n.commandResponse(tp, "QUIT"))
	payload := encodePayload(map[string]any{"protocol": "ftp", "target": target, "responses": compactLines(lines)})
	return &operationResult{
		payload:       payload,
		target:        target,
		dependency:    "network_ftp",
		bytesSent:     int64(len(username) + len(password) + len(command)),
		bytesReceived: int64(len(payload)),
		connectTime:   connectTime,
		messagesSent:  3,
		messagesRecv:  len(lines),
	}, firstResponseError(lines)
}
