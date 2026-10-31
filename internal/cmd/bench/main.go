package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/acneism/lua"
	"github.com/acneism/lua/internal/scenario"
)

type sample struct {
	RedisNS, GoNS      int64
	Allocations, Bytes uint64
}
type result struct {
	Name          string
	Source        string
	Repetitions   int
	Samples       []sample
	RedisNS, GoNS int64
	Ratio         float64
}
type report struct {
	GoVersion, OS, Architecture string
	CPUs                        int
	Results                     []result
}

func main() {
	address := flag.String("addr", "127.0.0.1:6390", "Redis 7.2.16 reference address")
	output := flag.String("output", "work/bench.json", "JSON report with every raw timing and median")
	rounds := flag.Int("rounds", 5, "alternating Redis and Go measurements per scenario")
	selected := flag.String("scenario", "", "single scenario name; empty runs all scenarios")
	profile := flag.String("cpuprofile", "", "optional Go CPU profile output file")
	flag.Parse()
	if err := run(*address, *output, *rounds, *selected, *profile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(address, output string, rounds int, selected, profile string) error {
	if rounds < 1 {
		return fmt.Errorf("rounds must be positive")
	}
	connection, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close() }()
	client := reference{connection, bufio.NewReader(connection)}
	info, err := client.command("INFO", "server")
	if err != nil {
		return err
	}
	if !strings.Contains(info, "redis_version:7.2.16\r\n") {
		return fmt.Errorf("redis 7.2.16 required")
	}
	info, err = client.command("INFO", "memory")
	if err != nil {
		return err
	}
	if !strings.Contains(info, "mem_allocator:jemalloc") {
		return fmt.Errorf("jemalloc reference build required")
	}
	if profile != "" {
		file, err := os.Create(profile)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		if err = pprof.StartCPUProfile(file); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}
	report := report{GoVersion: runtime.Version(), OS: runtime.GOOS, Architecture: runtime.GOARCH, CPUs: runtime.NumCPU()}
	for _, item := range scenario.All() {
		if selected != "" && selected != item.Name {
			continue
		}
		calibration := time.Now()
		if _, err = client.command("EVAL", item.Source, "0"); err != nil {
			return err
		}
		duration := time.Since(calibration)
		repetitions := max(1, int((35*time.Millisecond+duration-1)/duration))
		source := item.Source
		if repetitions > 1 {
			source = fmt.Sprintf("local function __benchmark__() %s end local __result for __round=1,%d do __result=__benchmark__() end return __result", source, repetitions)
		}
		proto, err := lua.Compile([]byte(source), "@benchmark")
		if err != nil {
			return err
		}
		state := lua.NewState(lua.Options{})
		function := state.Load(proto, nil)
		if _, err = state.Call(function); err != nil {
			return err
		}
		if _, err = client.command("EVAL", source, "0"); err != nil {
			return err
		}
		result := result{Name: item.Name, Source: source, Repetitions: repetitions}
		for round := 0; round < rounds; round++ {
			var entry sample
			var redisAnswer, goAnswer string
			redis := func() error {
				if _, err := client.command("EVAL", "collectgarbage('collect') return 0", "0"); err != nil {
					return err
				}
				start := time.Now()
				value, err := client.command("EVAL", source, "0")
				entry.RedisNS = time.Since(start).Nanoseconds()
				redisAnswer = value
				return err
			}
			goRun := func() error {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				start := time.Now()
				values, err := state.Call(function)
				entry.GoNS = time.Since(start).Nanoseconds()
				runtime.ReadMemStats(&after)
				entry.Allocations, entry.Bytes = after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc
				if err != nil {
					return err
				}
				if len(values) != 1 {
					return fmt.Errorf("unexpected Go result: %v", values)
				}
				goAnswer = strconv.FormatFloat(values[0].Float(), 'f', -1, 64)
				return nil
			}
			if round%2 == 0 {
				if err = redis(); err == nil {
					err = goRun()
				}
			} else {
				if err = goRun(); err == nil {
					err = redis()
				}
			}
			if err != nil {
				return err
			}
			if redisAnswer != goAnswer {
				return fmt.Errorf("%s result differs: Redis %s, Go %s", item.Name, redisAnswer, goAnswer)
			}
			result.Samples = append(result.Samples, entry)
			fmt.Printf("%s round %d Redis %.3fms Go %.3fms allocations %d bytes %d\n", item.Name, round+1, float64(entry.RedisNS)/1e6, float64(entry.GoNS)/1e6, entry.Allocations, entry.Bytes)
		}
		redisTimes, goTimes := make([]int64, rounds), make([]int64, rounds)
		for index, entry := range result.Samples {
			redisTimes[index], goTimes[index] = entry.RedisNS, entry.GoNS
		}
		slices.Sort(redisTimes)
		slices.Sort(goTimes)
		result.RedisNS, result.GoNS = redisTimes[rounds/2], goTimes[rounds/2]
		if result.RedisNS < int64(20*time.Millisecond) {
			return fmt.Errorf("%s Redis median is below 20 ms; enlarge the workload", item.Name)
		}
		result.Ratio = float64(result.GoNS) / float64(result.RedisNS)
		report.Results = append(report.Results, result)
		fmt.Printf("%s median Redis %.3fms Go %.3fms ratio %.3f\n", item.Name, float64(result.RedisNS)/1e6, float64(result.GoNS)/1e6, result.Ratio)
	}
	if len(report.Results) == 0 {
		return fmt.Errorf("unknown scenario %q", selected)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(data, '\n'), 0644)
}

type reference struct {
	net.Conn
	reader *bufio.Reader
}

func (r *reference) command(args ...string) (string, error) {
	if err := r.SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
		return "", err
	}
	var request strings.Builder
	fmt.Fprintf(&request, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&request, "$%d\r\n%s\r\n", len(arg), arg)
	}
	if _, err := r.Write([]byte(request.String())); err != nil {
		return "", err
	}
	line, err := r.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(line, "\r\n")
	switch line[0] {
	case '+', ':':
		return line[1:], nil
	case '-':
		return "", fmt.Errorf("redis: %s", line[1:])
	case '$':
		size, err := strconv.Atoi(line[1:])
		if err != nil {
			return "", err
		}
		if size < 0 {
			return "", fmt.Errorf("nil Redis reply")
		}
		data := make([]byte, size+2)
		for offset := 0; offset < len(data); {
			n, err := r.reader.Read(data[offset:])
			if err != nil {
				return "", err
			}
			offset += n
		}
		return string(data[:size]), nil
	default:
		return "", fmt.Errorf("unexpected Redis response %q", line)
	}
}
