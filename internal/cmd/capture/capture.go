package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const referenceVersion = "7.2.16"

const wrapper = `local function __capture__(ok, ...)
  if not ok then return "ERR " .. tostring((...)) end
  local n = select('#', ...)
  local out = {}
  for i = 1, n do out[i] = tostring((select(i, ...))) end
  return table.concat(out, ",")
end
local f, e = loadstring(%s, "@user_script")
if not f then return "ERR " .. tostring(e) end
return __capture__(pcall(f))`

type snippet struct {
	Src  string `json:"src"`
	Want string `json:"want"`
}

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(out)
	addr := flags.String("addr", "127.0.0.1:6390", "Redis 7.2.16 address")
	dest := flags.String("out", "", "capture output JSON file")
	verify := flags.Bool("verify", false, "verify recorded answers without writing files")
	timeout := flags.Duration("timeout", 30*time.Second, "deadline for each Redis request")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *timeout <= 0 {
		return errors.New("-timeout must be positive")
	}
	if (*verify && *dest != "") || (!*verify && *dest == "") {
		return errors.New("choose either -verify or -out FILE")
	}
	if flags.NArg() == 0 {
		return errors.New("provide at least one JSON input file")
	}
	cases, err := loadInputs(flags.Args(), *verify)
	if err != nil {
		return err
	}
	client, err := connect(*addr, *timeout)
	if err != nil {
		return err
	}
	defer func() { _ = client.conn.Close() }()
	if err := client.checkVersion(); err != nil {
		return err
	}
	mismatches := 0
	for i := range cases {
		got, err := client.command("EVAL", fmt.Sprintf(wrapper, quote(cases[i].Src)), "0")
		if err != nil {
			return fmt.Errorf("snippet %d: %w", i+1, err)
		}
		got = strings.TrimPrefix(got, "ERR ")
		if *verify {
			if got != cases[i].Want {
				mismatches++
				if _, err := fmt.Fprintf(out, "snippet %d %q: want %q, got %q\n", i+1, cases[i].Src, cases[i].Want, got); err != nil {
					return err
				}
			}
		} else {
			cases[i].Want = got
		}
	}
	if mismatches != 0 {
		return fmt.Errorf("%d of %d snippets differ from Redis %s", mismatches, len(cases), referenceVersion)
	}
	if *verify {
		_, err = fmt.Fprintf(out, "verified %d snippets against Redis %s\n", len(cases), referenceVersion)
		return err
	}
	if err := save(*dest, cases); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "captured %d snippets from Redis %s into %s\n", len(cases), referenceVersion, *dest)
	return err
}

func loadInputs(paths []string, verify bool) ([]snippet, error) {
	var cases []snippet
	seen := make(map[string]string)
	for _, path := range paths {
		list, err := load(path, verify)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, item := range list {
			if want, exists := seen[item.Src]; exists {
				if verify {
					return nil, fmt.Errorf("%s: duplicate source %q (previous answer %q)", path, item.Src, want)
				}
				continue
			}
			seen[item.Src] = item.Want
			cases = append(cases, item)
		}
	}
	if len(cases) == 0 {
		return nil, errors.New("input contains no snippets")
	}
	return cases, nil
}

func load(path string, verify bool) ([]snippet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	cases := make([]snippet, 0, len(raw))
	for i, entry := range raw {
		var src string
		if !verify && json.Unmarshal(entry, &src) == nil && string(entry) != "null" {
			cases = append(cases, snippet{Src: src})
			continue
		}
		var item struct {
			Src  *string `json:"src"`
			Want *string `json:"want"`
		}
		decoder := json.NewDecoder(bytes.NewReader(entry))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&item); err != nil {
			return nil, fmt.Errorf("entry %d: %w", i+1, err)
		}
		if item.Src == nil || (verify && item.Want == nil) {
			return nil, fmt.Errorf("entry %d requires src%s", i+1, requiredAnswer(verify))
		}
		current := snippet{Src: *item.Src}
		if item.Want != nil {
			current.Want = *item.Want
		}
		cases = append(cases, current)
	}
	return cases, nil
}

func requiredAnswer(verify bool) string {
	if verify {
		return " and want"
	}
	return ""
}

func quote(src string) string {
	var out strings.Builder
	out.WriteByte('"')
	for i := 0; i < len(src); i++ {
		switch c := src[i]; {
		case c == '"':
			out.WriteString(`\"`)
		case c == '\\':
			out.WriteString(`\\`)
		case c == '\n':
			out.WriteString(`\n`)
		case c == '\r':
			out.WriteString(`\r`)
		case c < 32 || c == 127:
			fmt.Fprintf(&out, `\%03d`, c)
		default:
			out.WriteByte(c)
		}
	}
	out.WriteByte('"')
	return out.String()
}

func save(path string, cases []snippet) error {
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(cases); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".capture-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data.Bytes()); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
