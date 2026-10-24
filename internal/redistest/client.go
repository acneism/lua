package redistest

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/acneism/lua/redis"
)

type Client struct {
	mu         sync.Mutex
	connection net.Conn
	reader     *bufio.Reader
}

func Dial(address string) (*Client, error) {
	connection, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return &Client{connection: connection, reader: bufio.NewReader(connection)}, nil
}

func (c *Client) Close() error { return c.connection.Close() }

func (c *Client) Do(arguments ...string) (redis.Reply, error) {
	argv := make([][]byte, len(arguments))
	for i, argument := range arguments {
		argv[i] = []byte(argument)
	}
	return c.Command(argv)
}

func (c *Client) Command(argv [][]byte) (redis.Reply, error) {
	reply, _, err := c.command(argv, false)
	return reply, err
}

func (c *Client) CommandWire(argv [][]byte) (redis.Reply, []byte, error) {
	return c.command(argv, true)
}

func (c *Client) command(argv [][]byte, wire bool) (redis.Reply, []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.connection.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return redis.Reply{}, nil, err
	}
	var request strings.Builder
	fmt.Fprintf(&request, "*%d\r\n", len(argv))
	for _, argument := range argv {
		fmt.Fprintf(&request, "$%d\r\n%s\r\n", len(argument), argument)
	}
	if _, err := c.connection.Write([]byte(request.String())); err != nil {
		return redis.Reply{}, nil, err
	}
	if !wire {
		reply, err := redis.ReadReply(c.reader)
		return reply, nil, err
	}
	var captured bytes.Buffer
	reader := bufio.NewReader(io.TeeReader(c.reader, &captured))
	reply, err := redis.ReadReply(reader)
	if reader.Buffered() != 0 {
		return reply, nil, fmt.Errorf("unexpected pipelined reference reply")
	}
	return reply, captured.Bytes(), err
}

func (c *Client) RequireVersion() error {
	reply, err := c.Do("INFO", "server")
	if err != nil {
		return err
	}
	if !strings.Contains(reply.Str, "redis_version:7.2.16\r\n") {
		return fmt.Errorf("redis 7.2.16 required")
	}
	return nil
}
