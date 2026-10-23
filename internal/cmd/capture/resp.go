package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const maxBulkReply = 64 << 20

type referenceClient struct {
	conn    net.Conn
	reader  *bufio.Reader
	timeout time.Duration
}

func connect(addr string, timeout time.Duration) (*referenceClient, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	return &referenceClient{conn: conn, reader: bufio.NewReader(conn), timeout: timeout}, nil
}

func (c *referenceClient) checkVersion() error {
	info, err := c.command("INFO", "server")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(info, "\r\n") {
		if version, ok := strings.CutPrefix(line, "redis_version:"); ok {
			if version != referenceVersion {
				return fmt.Errorf("reference must be Redis %s, got %s", referenceVersion, version)
			}
			return nil
		}
	}
	return errors.New("INFO server did not report redis_version")
}

func (c *referenceClient) command(args ...string) (string, error) {
	if err := c.conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return "", err
	}
	var request bytes.Buffer
	fmt.Fprintf(&request, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&request, "$%d\r\n%s\r\n", len(arg), arg)
	}
	if _, err := request.WriteTo(c.conn); err != nil {
		return "", err
	}
	return readBulk(c.reader)
}

func readBulk(reader *bufio.Reader) (string, error) {
	header, err := reader.ReadSlice('\n')
	if err != nil {
		return "", err
	}
	if len(header) < 3 || !bytes.HasSuffix(header, []byte("\r\n")) {
		return "", errors.New("invalid RESP header")
	}
	if header[0] == '-' {
		return "", fmt.Errorf("redis: %s", header[1:len(header)-2])
	}
	if header[0] != '$' {
		return "", fmt.Errorf("expected bulk reply, got %q", header[:len(header)-2])
	}
	size, err := strconv.Atoi(string(header[1 : len(header)-2]))
	if err != nil || size < 0 || size > maxBulkReply {
		return "", fmt.Errorf("invalid bulk length %q", header[1:len(header)-2])
	}
	data := make([]byte, size+2)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", err
	}
	if !bytes.Equal(data[size:], []byte("\r\n")) {
		return "", errors.New("invalid bulk terminator")
	}
	return string(data[:size]), nil
}
