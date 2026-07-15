package network

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func (n *Network) runSMTP(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	target, err := n.endpoint(action, "25")
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
	if _, line, err := tp.ReadResponse(220); err == nil {
		lines = append(lines, line)
	} else {
		return nil, err
	}
	hostname := protocol.FirstNonEmpty(actionParamString(action.Params, "helo"), "apexload.local")
	lines = append(lines, smtpCommandResponse(tp, "EHLO "+hostname, 250))
	if n.username != "" || actionParamString(action.Params, "username") != "" {
		username := protocol.FirstNonEmpty(actionParamString(action.Params, "username"), n.username)
		password := protocol.FirstNonEmpty(actionParamString(action.Params, "password"), n.password)
		auth := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
		lines = append(lines, smtpCommandResponse(tp, "AUTH PLAIN "+auth, 235))
	}
	from := protocol.FirstNonEmpty(actionParamString(action.Params, "from"), n.from)
	if from == "" {
		return nil, errors.New("smtp from address is required")
	}
	to := actionParamStringSlice(action.Params, "to")
	if len(to) == 0 {
		to = n.to
	}
	if len(to) == 0 {
		return nil, errors.New("smtp recipient is required")
	}
	lines = append(lines, smtpCommandResponse(tp, "MAIL FROM:<"+from+">", 250))
	for _, recipient := range to {
		lines = append(lines, smtpCommandResponse(tp, "RCPT TO:<"+recipient+">", 250))
	}
	if err := tp.PrintfLine("DATA"); err != nil {
		return nil, err
	}
	lines = append(lines, mustReadResponse(tp, 354))
	message := smtpMessage(from, to, action)
	writer := tp.DotWriter()
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	lines = append(lines, mustReadResponse(tp, 250))
	lines = append(lines, smtpCommandResponse(tp, "QUIT", 221))
	payload := encodePayload(map[string]any{"protocol": "smtp", "target": target, "from": from, "to": to, "responses": compactLines(lines)})
	return &operationResult{
		payload:       payload,
		target:        target,
		dependency:    "network_smtp",
		bytesSent:     int64(len(message)),
		bytesReceived: int64(len(payload)),
		connectTime:   connectTime,
		messagesSent:  len(to) + 5,
		messagesRecv:  len(lines),
	}, firstResponseError(lines)
}

func (n *Network) runPOP3(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	target, err := n.endpoint(action, "110")
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
	username := protocol.FirstNonEmpty(actionParamString(action.Params, "username"), n.username)
	password := protocol.FirstNonEmpty(actionParamString(action.Params, "password"), n.password)
	if username != "" {
		lines = append(lines, n.commandResponse(tp, "USER "+username))
		if password != "" {
			lines = append(lines, n.commandResponse(tp, "PASS "+password))
		}
	}
	command := protocol.FirstNonEmpty(actionParamString(action.Params, "command"), strings.TrimSpace(action.Value), string(action.Body), action.Target)
	if command == "" || strings.Contains(command, ":") {
		command = "STAT"
	}
	lines = append(lines, n.commandResponse(tp, command))
	lines = append(lines, n.commandResponse(tp, "QUIT"))
	payload := encodePayload(map[string]any{"protocol": "pop3", "target": target, "responses": compactLines(lines)})
	return &operationResult{
		payload:       payload,
		target:        target,
		dependency:    "network_pop3",
		bytesSent:     int64(len(command)),
		bytesReceived: int64(len(payload)),
		connectTime:   connectTime,
		messagesSent:  2,
		messagesRecv:  len(lines),
	}, firstResponseError(lines)
}

func (n *Network) runIMAP(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	target, err := n.endpoint(action, "143")
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
	tagID := 1
	username := protocol.FirstNonEmpty(actionParamString(action.Params, "username"), n.username)
	password := protocol.FirstNonEmpty(actionParamString(action.Params, "password"), n.password)
	if username != "" {
		tag := fmt.Sprintf("A%03d", tagID)
		tagID++
		if err := tp.PrintfLine("%s LOGIN %s %s", tag, username, password); err != nil {
			return nil, err
		}
		loginLines, err := readIMAPTaggedResponse(tp, tag)
		if err != nil {
			return nil, err
		}
		lines = append(lines, loginLines...)
	}
	command := protocol.FirstNonEmpty(actionParamString(action.Params, "command"), strings.TrimSpace(action.Value), string(action.Body), "CAPABILITY")
	tag := fmt.Sprintf("A%03d", tagID)
	tagID++
	if err := tp.PrintfLine("%s %s", tag, command); err != nil {
		return nil, err
	}
	commandLines, err := readIMAPTaggedResponse(tp, tag)
	if err != nil {
		return nil, err
	}
	lines = append(lines, commandLines...)
	logoutTag := fmt.Sprintf("A%03d", tagID)
	_ = tp.PrintfLine("%s LOGOUT", logoutTag)
	logoutLines, _ := readIMAPTaggedResponse(tp, logoutTag)
	lines = append(lines, logoutLines...)
	payload := encodePayload(map[string]any{"protocol": "imap", "target": target, "responses": compactLines(lines)})
	return &operationResult{
		payload:       payload,
		target:        target,
		dependency:    "network_imap",
		bytesSent:     int64(len(command)),
		bytesReceived: int64(len(payload)),
		connectTime:   connectTime,
		messagesSent:  tagID,
		messagesRecv:  len(lines),
	}, firstResponseError(lines)
}
