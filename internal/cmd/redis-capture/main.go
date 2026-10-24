package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/acneism/lua/internal/redistest"
)

func main() {
	address := flag.String("addr", "127.0.0.1:6393", "Redis 7.2.16 reference address")
	output := flag.String("out", "", "output fixture path; required unless -verify")
	verify := flag.Bool("verify", false, "verify all existing answers without writing")
	flag.Parse()
	if err := run(*address, *output, *verify, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(address, output string, verify bool, files []string) error {
	if len(files) == 0 || !verify && output == "" {
		return fmt.Errorf("input fixtures and -out or -verify required")
	}
	client, err := redistest.Dial(address)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err = client.RequireVersion(); err != nil {
		return err
	}
	var captured []redistest.Case
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var entries []redistest.Case
		if err = json.Unmarshal(data, &entries); err != nil {
			return err
		}
		for index, item := range entries {
			proto := item.Proto
			if proto != 3 {
				proto = 2
			}
			if _, err = client.Do("HELLO", fmt.Sprint(proto)); err != nil {
				return err
			}
			if err = redistest.Prepare(client, item.Setup); err != nil {
				return err
			}
			_, wire, err := item.ReferenceWire(client)
			if err != nil {
				return err
			}
			actual := wire
			if verify && !bytes.Equal(item.Want, actual) {
				return fmt.Errorf("%s[%d] %s: want %q, got %q", file, index, item.Name, item.Want, actual)
			}
			item.Want = actual
			previousTrace, _ := json.Marshal(item.Trace)
			if err = item.CaptureTrace(client); err != nil {
				return err
			}
			currentTrace, _ := json.Marshal(item.Trace)
			if verify && !bytes.Equal(previousTrace, currentTrace) {
				return fmt.Errorf("%s[%d] host trace changed", file, index)
			}
			captured = append(captured, item)
		}
	}
	if !verify {
		data, err := json.MarshalIndent(captured, "", "  ")
		if err != nil {
			return err
		}
		temporary, err := os.CreateTemp(filepath.Dir(output), ".redis-capture-*")
		if err != nil {
			return err
		}
		name := temporary.Name()
		defer func() { _ = os.Remove(name) }()
		if _, err = temporary.Write(append(data, '\n')); err != nil {
			_ = temporary.Close()
			return err
		}
		if err = temporary.Close(); err != nil {
			return err
		}
		if err = os.Rename(name, output); err != nil {
			return err
		}
	}
	fmt.Printf("%d engine cases %s against Redis 7.2.16\n", len(captured), map[bool]string{true: "verified", false: "captured"}[verify])
	return nil
}
