package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestReadBulk(t *testing.T) {
	for _, want := range []string{"", "a\x00b\r\n", strings.Repeat("x", 10000)} {
		got, err := readBulk(bufio.NewReader(strings.NewReader(bulk(want))))
		if err != nil || got != want {
			t.Fatalf("got %q, %v", got, err)
		}
	}
}

func TestReadBulkRejectsMalformedReplies(t *testing.T) {
	for _, response := range []string{
		"", "$1\r", "\n", "$1\n", "+OK\r\n", "-ERR wire fixture\r\n",
		"$-1\r\n", "$bad\r\n", "$9999999999999999999999\r\n",
		fmt.Sprintf("$%d\r\n", maxBulkReply+1), "$1\r\na", "$1\r\naXX",
		strings.Repeat("x", 5000) + "\r\n",
	} {
		if _, err := readBulk(bufio.NewReader(strings.NewReader(response))); err == nil {
			t.Errorf("accepted %q", response)
		}
	}
}

func TestRequestDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	c := referenceClient{conn: client, reader: bufio.NewReader(client), timeout: 10 * time.Millisecond}
	_, err := c.command("INFO", "server")
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("got %v, want a deadline error", err)
	}
}

func TestClosedConnectionDeadline(t *testing.T) {
	client, server := net.Pipe()
	_ = client.Close()
	_ = server.Close()
	c := referenceClient{conn: client, reader: bufio.NewReader(client), timeout: time.Second}
	if _, err := c.command("INFO", "server"); err == nil {
		t.Fatal("accepted closed connection")
	}
}

func TestVersionCommandError(t *testing.T) {
	addr, _ := reference(t, "", nil)
	client, err := connect(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.conn.Close() }()
	if err := client.checkVersion(); err == nil {
		t.Fatal("accepted absent version")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestOutputFailure(t *testing.T) {
	item := corpus(t)[0]
	path := inputFile(t, encoded(t, []snippet{item}))
	addr, _ := reference(t, "redis_version:7.2.16\r\n", []string{bulk(item.Want)})
	if err := run([]string{"-addr", addr, "-verify", path}, failingWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("got %v", err)
	}
}

func TestCaptureRejectsNonUTF8(t *testing.T) {
	path := inputFile(t, encoded(t, corpus(t)[:1]))
	dest := inputFile(t, "preserve me")
	addr, _ := reference(t, "redis_version:7.2.16\r\n", []string{bulk(string([]byte{255}))})
	err := run([]string{"-addr", addr, "-out", dest, path}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Fatalf("got %v", err)
	}
}
