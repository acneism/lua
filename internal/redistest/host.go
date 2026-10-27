package redistest

import (
	"fmt"
	"strings"
	"sync"

	"github.com/acneism/lua/redis"
)

type metadata struct {
	command            redis.Command
	found, subcommands bool
}
type Log struct {
	Level   int
	Message string
}
type Host struct {
	Client   *Client
	User     string
	mu       sync.Mutex
	commands map[string]metadata
	proto    int
	Logs     []Log
}

func (h *Host) lookup(name string) metadata {
	if h.commands == nil {
		h.commands = make(map[string]metadata)
	}
	if cached, ok := h.commands[name]; ok {
		return cached
	}
	reply, err := h.Client.Do("COMMAND", "INFO", name)
	if err != nil {
		panic(err)
	}
	result := metadata{}
	if len(reply.Elements) > 0 && reply.Elements[0].Type == redis.ArrayReply {
		fields := reply.Elements[0].Elements
		if len(fields) >= 3 {
			result.command = redis.Command{Name: fields[0].Str, Arity: int(fields[1].Int), MayReplicate: name == "pfcount" || name == "publish" || name == "spublish"}
			result.found = true
			for _, flag := range fields[2].Elements {
				switch flag.Str {
				case "write":
					result.command.Write = true
				case "noscript":
					result.command.NoScript = true
				}
			}
			result.subcommands = len(fields) > 9 && len(fields[9].Elements) > 0
		}
	}
	h.commands[name] = result
	return result
}

func (h *Host) Lookup(argv [][]byte) (redis.Command, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(argv) == 0 {
		return redis.Command{}, false
	}
	name := strings.ToLower(string(argv[0]))
	result := h.lookup(name)
	if result.subcommands && len(argv) > 1 {
		result = h.lookup(name + "|" + strings.ToLower(string(argv[1])))
	}
	return result.command, result.found
}

func (h *Host) Check(argv [][]byte) *redis.Reply {
	if h.User == "" {
		return nil
	}
	command := [][]byte{[]byte("ACL"), []byte("DRYRUN"), []byte(h.User)}
	command = append(command, argv...)
	reply, err := h.Client.Command(command)
	if err != nil {
		failure := redis.Abort(err)
		return &failure
	}
	if reply.Type == redis.ErrorReply {
		reply.Str = "NOPERM ACL failure in script: " + strings.TrimPrefix(reply.Str, "ERR ")
		return &reply
	}
	return nil
}

func (h *Host) Call(argv [][]byte, proto int) redis.Reply {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.proto == 0 {
		h.proto = 2
	}
	if h.proto != proto {
		reply, err := h.Client.Do("HELLO", fmt.Sprint(proto))
		if err != nil {
			return redis.Abort(err)
		}
		if reply.Type == redis.ErrorReply {
			return reply
		}
		h.proto = proto
	}
	reply, err := h.Client.Command(argv)
	if err != nil {
		return redis.Abort(err)
	}
	return reply
}

func (h *Host) Log(level int, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Logs = append(h.Logs, Log{level, message})
}
