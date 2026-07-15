package network

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
)

const (
	hardNetworkTextLineBytes     = 64 << 10
	hardNetworkTextResponseBytes = 1 << 20
	hardNetworkTextLines         = 4_096
)

// boundedProtocolReader places limits below net/textproto. textproto.ReadLine
// otherwise joins arbitrarily many bufio fragments, and ReadResponse joins an
// arbitrary multiline response before the protocol adapter can inspect it.
type boundedProtocolReader struct {
	source      *bufio.Reader
	pending     []byte
	terminalErr error
	totalBytes  int64
	lines       int
}

func newBoundedProtocolReader(source io.Reader) *boundedProtocolReader {
	return &boundedProtocolReader{source: bufio.NewReaderSize(source, hardNetworkTextLineBytes+1)}
}

func (reader *boundedProtocolReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if len(reader.pending) == 0 {
		if reader.terminalErr != nil {
			return 0, reader.terminalErr
		}
		line, terminalErr, err := reader.nextLine()
		if err != nil {
			return 0, err
		}
		reader.pending = line
		reader.terminalErr = terminalErr
		if len(reader.pending) == 0 && reader.terminalErr != nil {
			return 0, reader.terminalErr
		}
	}
	n := copy(dst, reader.pending)
	reader.pending = reader.pending[n:]
	return n, nil
}

func (reader *boundedProtocolReader) nextLine() ([]byte, error, error) {
	line := make([]byte, 0, 256)
	for {
		fragment, err := reader.source.ReadSlice('\n')
		if int64(len(fragment)) > int64(hardNetworkTextResponseBytes)-reader.totalBytes {
			return nil, nil, fmt.Errorf("text protocol response exceeds %d bytes", hardNetworkTextResponseBytes)
		}
		if len(fragment) > hardNetworkTextLineBytes-len(line) {
			return nil, nil, fmt.Errorf("text protocol response line exceeds %d bytes", hardNetworkTextLineBytes)
		}
		reader.totalBytes += int64(len(fragment))
		line = append(line, fragment...)
		switch {
		case err == nil:
			reader.lines++
			if reader.lines > hardNetworkTextLines {
				return nil, nil, fmt.Errorf("text protocol response exceeds %d lines", hardNetworkTextLines)
			}
			return line, nil, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) > 0 {
				reader.lines++
				if reader.lines > hardNetworkTextLines {
					return nil, nil, fmt.Errorf("text protocol response exceeds %d lines", hardNetworkTextLines)
				}
				return line, io.EOF, nil
			}
			return nil, io.EOF, nil
		default:
			if len(line) > 0 {
				return line, err, nil
			}
			return nil, nil, err
		}
	}
}

type boundedTextReadWriteCloser struct {
	reader *boundedProtocolReader
	conn   net.Conn
}

func (conn *boundedTextReadWriteCloser) Read(data []byte) (int, error) {
	return conn.reader.Read(data)
}

func (conn *boundedTextReadWriteCloser) Write(data []byte) (int, error) {
	return conn.conn.Write(data)
}

func (conn *boundedTextReadWriteCloser) Close() error { return conn.conn.Close() }

func newBoundedTextProtoConn(conn net.Conn) *textproto.Conn {
	return textproto.NewConn(&boundedTextReadWriteCloser{reader: newBoundedProtocolReader(conn), conn: conn})
}
