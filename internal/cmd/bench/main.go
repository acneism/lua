package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/acneism/lua"
	"github.com/acneism/lua/internal/scenario"
	"github.com/acneism/lua/redis"
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
	Processes     []float64
}
type report struct {
	GoVersion, OS, Architecture string
	CPUs                        int
	Results                     []result
}

type memoryHost struct {
	counters map[string]int64
	hashes   map[string]map[string]string
}

func (h *memoryHost) Lookup(argv [][]byte) (redis.Command, bool) {
	return redis.Command{Name: string(argv[0]), Arity: -2, Write: true}, true
}

func (h *memoryHost) Check([][]byte) *redis.Reply { return nil }

func (h *memoryHost) Log(int, string) {}

func (h *memoryHost) Call(argv [][]byte, _ int) redis.Reply {
	if string(argv[0]) == "INCR" {
		h.counters[string(argv[1])]++
		return redis.Integer(h.counters[string(argv[1])])
	}
	hash := h.hashes[string(argv[1])]
	if hash == nil {
		hash = make(map[string]string)
		h.hashes[string(argv[1])] = hash
	}
	hash[string(argv[2])] = string(argv[3])
	return redis.Integer(1)
}

func main() {
	address := flag.String("addr", "127.0.0.1:6390", "Redis 7.2.16 reference address")
	output := flag.String("output", "work/bench.json", "JSON report with every raw timing and median")
	rounds := flag.Int("rounds", 5, "alternating Redis and Go measurements per scenario")
	selected := flag.String("scenario", "", "single scenario name; empty runs all scenarios")
	profile := flag.String("cpuprofile", "", "optional Go CPU profile output file")
	processes := flag.Int("processes", 5, "fresh processes per scenario; the report keeps the median one. A profile uses one process")
	flag.Parse()
	var err error
	if *processes > 1 && *profile == "" {
		err = runProcesses(*address, *output, *rounds, *selected, *processes)
	} else {
		err = run(*address, *output, *rounds, *selected, *profile)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runProcesses(address, output string, rounds int, selected string, count int) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "bench")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	reports := make([]report, count)
	for index := range reports {
		path := filepath.Join(directory, strconv.Itoa(index)+".json")
		command := exec.Command(executable, "-addr", address, "-rounds", strconv.Itoa(rounds), "-scenario", selected, "-processes", "1", "-output", path)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err = command.Run(); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &reports[index]); err != nil {
			return err
		}
	}
	merged := reports[0]
	merged.Results = make([]result, len(reports[0].Results))
	for index := range merged.Results {
		candidates := make([]result, count)
		ratios := make([]float64, count)
		for process := range reports {
			candidates[process] = reports[process].Results[index]
			ratios[process] = candidates[process].Ratio
		}
		slices.SortFunc(candidates, func(a, b result) int { return cmp.Compare(a.Ratio, b.Ratio) })
		merged.Results[index] = candidates[count/2]
		merged.Results[index].Processes = ratios
		fmt.Printf("%s median of %d processes ratio %.3f from %.3f\n", merged.Results[index].Name, count, merged.Results[index].Ratio, ratios)
	}
	return writeReport(output, merged)
}

func writeReport(output string, report report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(data, '\n'), 0644)
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
	core := len(scenario.All())
	for index, item := range append(scenario.All(), scenario.Library()...) {
		if selected != "" && selected != item.Name {
			continue
		}
		duration := time.Hour
		for range 3 {
			calibration := time.Now()
			if _, err = client.command("EVAL", item.Source, "0"); err != nil {
				return err
			}
			duration = min(duration, time.Since(calibration))
		}
		repetitions := max(1, int((35*time.Millisecond+duration-1)/duration))
		source := item.Source
		if repetitions > 1 {
			source = fmt.Sprintf("local function __benchmark__() %s end local __result for __round=1,%d do __result=__benchmark__() end return __result", source, repetitions)
		}
		var execute func() (string, error)
		if index < core {
			proto, err := lua.Compile([]byte(source), "@benchmark")
			if err != nil {
				return err
			}
			state := lua.NewState(lua.Options{})
			function := state.Load(proto, nil)
			execute = func() (string, error) {
				values, err := state.Call(function)
				if err != nil {
					return "", err
				}
				if len(values) != 1 {
					return "", fmt.Errorf("unexpected Go result: %v", values)
				}
				return strconv.FormatFloat(values[0].Float(), 'f', -1, 64), nil
			}
		} else {
			engine := redis.NewEngine(redis.Config{})
			script := []byte(source)
			host := &memoryHost{counters: make(map[string]int64), hashes: make(map[string]map[string]string)}
			execute = func() (string, error) {
				reply := engine.Eval(script, nil, nil, redis.Run{Host: host})
				if reply.Type != redis.IntegerReply {
					return "", fmt.Errorf("unexpected Go reply: %+v", reply)
				}
				return strconv.FormatInt(reply.Int, 10), nil
			}
		}
		if _, err = execute(); err != nil {
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
				answer, err := execute()
				entry.GoNS = time.Since(start).Nanoseconds()
				runtime.ReadMemStats(&after)
				entry.Allocations, entry.Bytes = after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc
				goAnswer = answer
				return err
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
	return writeReport(output, report)
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
