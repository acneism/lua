package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func corpus(t *testing.T) []snippet {
	t.Helper()
	cases, err := load(filepath.Join("..", "..", "..", "testdata", "snippets.json"), true)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func inputFile(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func encoded(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func bulk(value string) string {
	return fmt.Sprintf("$%d\r\n%s\r\n", len(value), value)
}

func readRequest(reader *bufio.Reader) ([]string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(header, "*") || !strings.HasSuffix(header, "\r\n") {
		return nil, fmt.Errorf("invalid array header %q", header)
	}
	count, err := strconv.Atoi(strings.TrimSuffix(header[1:], "\r\n"))
	if err != nil || count < 0 || count > 10 {
		return nil, fmt.Errorf("invalid request length %q", header)
	}
	args := make([]string, count)
	for i := range args {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(line, "$") || !strings.HasSuffix(line, "\r\n") {
			return nil, fmt.Errorf("invalid argument header %q", line)
		}
		size, err := strconv.Atoi(strings.TrimSuffix(line[1:], "\r\n"))
		if err != nil || size < 0 || size > 1<<20 {
			return nil, fmt.Errorf("invalid argument size %q", line)
		}
		data := make([]byte, size+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		if string(data[size:]) != "\r\n" {
			return nil, errors.New("invalid request terminator")
		}
		args[i] = string(data[:size])
	}
	return args, nil
}

func reference(t *testing.T, version string, replies []string) (string, <-chan []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan []string, len(replies)+1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(requests)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Error(err)
			return
		}
		reader := bufio.NewReader(conn)
		responses := append([]string{bulk(version)}, replies...)
		for _, response := range responses {
			args, err := readRequest(reader)
			if err != nil {
				t.Error(err)
				return
			}
			requests <- args
			if _, err := io.WriteString(conn, response); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return listener.Addr().String(), requests
}

func TestCorpusIsComplete(t *testing.T) {
	cases, err := loadInputs([]string{filepath.Join("..", "..", "..", "testdata", "snippets.json")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 707 {
		t.Fatalf("got %d snippets, want at least 707", len(cases))
	}
}

func TestQuote(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{"", `""`},
		{"a\"b\\c\nd\re", `"a\"b\\c\nd\re"`},
		{"\x001\x012\t3\x7f4", `"\0001\0012\0093\1274"`},
		{"кириллица\xff", "\"кириллица\xff\""},
	}
	for _, test := range tests {
		if got := quote(test.src); got != test.want {
			t.Errorf("quote(%q) = %q, want %q", test.src, got, test.want)
		}
	}
}

func TestVerifyPreservesCorpusAndWrapper(t *testing.T) {
	item := corpus(t)[0]
	data := encoded(t, []snippet{item})
	path := inputFile(t, data)
	addr, requests := reference(t, "# Server\r\nredis_version:7.2.16\r\n", []string{bulk(item.Want)})
	var out bytes.Buffer
	if err := run([]string{"-addr", addr, "-verify", path}, &out); err != nil {
		t.Fatal(err)
	}
	if got := <-requests; len(got) != 2 || got[0] != "INFO" || got[1] != "server" {
		t.Fatalf("unexpected version request %q", got)
	}
	if got := <-requests; len(got) != 3 || got[0] != "EVAL" || got[1] != fmt.Sprintf(wrapper, quote(item.Src)) || got[2] != "0" {
		t.Fatalf("unexpected eval request %q", got)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != data {
		t.Fatalf("verification changed input: %q, %v", unchanged, err)
	}
	if !strings.Contains(out.String(), "verified 1 snippets") {
		t.Fatal(out.String())
	}
}

func TestVerifyRejectsEveryMismatch(t *testing.T) {
	cases := corpus(t)[:2]
	path := inputFile(t, encoded(t, cases))
	addr, _ := reference(t, "redis_version:7.2.16\r\n", []string{bulk("transport-mismatch-1"), bulk("transport-mismatch-2")})
	var out bytes.Buffer
	err := run([]string{"-addr", addr, "-verify", path}, &out)
	if err == nil || !strings.Contains(err.Error(), "2 of 2 snippets differ") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(out.String(), "snippet 1") || !strings.Contains(out.String(), "snippet 2") {
		t.Fatal(out.String())
	}
}

func TestCaptureNormalizesAndDeduplicates(t *testing.T) {
	item := corpus(t)[0]
	path := inputFile(t, encoded(t, []string{item.Src, item.Src}))
	dest := inputFile(t, "old output")
	addr, _ := reference(t, "redis_version:7.2.16\r\n", []string{bulk("ERR " + item.Want)})
	var out bytes.Buffer
	if err := run([]string{"-addr", addr, "-out", dest, path}, &out); err != nil {
		t.Fatal(err)
	}
	got, err := load(dest, true)
	if err != nil || len(got) != 1 || got[0] != item {
		t.Fatalf("got %v, %v", got, err)
	}
	if !strings.Contains(out.String(), "captured 1 snippets") {
		t.Fatal(out.String())
	}
}

func TestCaptureDoesNotOverwriteOnFailure(t *testing.T) {
	path := inputFile(t, encoded(t, corpus(t)[:2]))
	dest := inputFile(t, "preserve me")
	addr, _ := reference(t, "redis_version:7.2.16\r\n", []string{bulk("first reply"), "-ERR transport fixture\r\n"})
	err := run([]string{"-addr", addr, "-out", dest, path}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "snippet 2") {
		t.Fatalf("got %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "preserve me" {
		t.Fatalf("output changed: %q, %v", data, err)
	}
}

func TestRejectsWrongReferenceVersion(t *testing.T) {
	for _, test := range []struct {
		info string
		want string
	}{
		{"redis_version:7.2.15\r\n", "reference must be Redis 7.2.16, got 7.2.15"},
		{"# Server\r\n", "INFO server did not report redis_version"},
	} {
		path := inputFile(t, encoded(t, corpus(t)[:1]))
		addr, requests := reference(t, test.info, nil)
		if err := run([]string{"-addr", addr, "-verify", path}, io.Discard); err == nil || err.Error() != test.want {
			t.Fatalf("got %v, want %q", err, test.want)
		}
		for request := range requests {
			if len(request) == 0 || request[0] != "INFO" {
				t.Fatalf("ran snippet before version check: %q", request)
			}
		}
	}
}

func TestInvalidOptionsAndInputs(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-unknown"}, {"-verify", "-out", "out.json"},
		{"-verify"}, {"-out", "out.json"}, {"-verify", "-timeout", "0"},
		{"-verify", "does-not-exist.json"}, {"-verify", "-timeout", "bad"},
	} {
		if err := run(args, io.Discard); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
	if err := run([]string{"-help"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		`{}`, `null`, `[]`, `not json`, `["source"]`, `[{}]`, `[null]`,
		`[{"src":"s"}]`, `[{"src":null,"want":"w"}]`,
		`[{"src":"s","want":null}]`, `[{"src":"s","want":"w","extra":true}]`,
		`[{"src":"s","want":"w"},{"src":"s","want":"w"}]`,
	} {
		path := inputFile(t, data)
		if err := run([]string{"-verify", path}, io.Discard); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
}

func TestLoadCaptureObjects(t *testing.T) {
	path := inputFile(t, `[{"src":"a"},{"src":"b","want":"old"}]`)
	got, err := load(path, false)
	if err != nil || len(got) != 2 || got[0].Src != "a" || got[1].Want != "old" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := load(inputFile(t, `[{"want":"old"}]`), false); err == nil {
		t.Fatal("accepted missing source")
	}
}

func TestSaveFailure(t *testing.T) {
	if err := save(filepath.Join(t.TempDir(), "missing", "out.json"), nil); err == nil {
		t.Fatal("accepted missing destination directory")
	}
	dir := t.TempDir()
	if err := save(dir, nil); err == nil {
		t.Fatal("replaced directory with corpus")
	}
}

func TestRunConnectionFailure(t *testing.T) {
	path := inputFile(t, encoded(t, corpus(t)[:1]))
	if err := run([]string{"-addr", "invalid-address", "-verify", path}, io.Discard); err == nil {
		t.Fatal("accepted invalid address")
	}
}
