package redistest

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/acneism/lua/redis"
)

type Case struct {
	Name     string     `json:"name"`
	Source   string     `json:"src"`
	Keys     []string   `json:"keys,omitempty"`
	Args     []string   `json:"args,omitempty"`
	Setup    [][]string `json:"setup,omitempty"`
	ReadOnly bool       `json:"readonly,omitempty"`
	Proto    int        `json:"proto,omitempty"`
	Want     []byte     `json:"want"`
	Trace    []Trace    `json:"trace,omitempty"`
	Request  []string   `json:"request,omitempty"`
}

type Trace struct {
	Argv    []string      `json:"argv"`
	Call    bool          `json:"call,omitempty"`
	Proto   int           `json:"proto,omitempty"`
	Command redis.Command `json:"command"`
	Found   bool          `json:"found"`
	Reply   []byte        `json:"reply,omitempty"`
}

func (item *Case) CaptureTrace(client *Client) error {
	if len(item.Trace) == 0 {
		return nil
	}
	if err := Prepare(client, item.Setup); err != nil {
		return err
	}
	host := Host{Client: client}
	for i := range item.Trace {
		step := &item.Trace[i]
		argv := make([][]byte, len(step.Argv))
		for j, arg := range step.Argv {
			argv[j] = []byte(arg)
		}
		step.Command, step.Found = host.Lookup(argv)
		if step.Call {
			proto := step.Proto
			if proto != 3 {
				proto = 2
			}
			if _, err := client.Do("HELLO", strconv.Itoa(proto)); err != nil {
				return err
			}
			if _, err := client.Do("MULTI"); err != nil {
				return err
			}
			if _, err := client.Command(argv); err != nil {
				return err
			}
			reply, wire, err := client.CommandWire([][]byte{[]byte("EXEC")})
			if err != nil {
				return err
			}
			if reply.Type != redis.ArrayReply || len(reply.Elements) != 1 {
				return fmt.Errorf("unexpected EXEC trace reply")
			}
			step.Reply = wire[bytes.Index(wire, []byte("\r\n"))+2:]
		}
	}
	return nil
}

func Prepare(client *Client, commands [][]string) error {
	if reply, err := client.Do("FLUSHDB"); err != nil {
		return err
	} else if reply.Type == redis.ErrorReply {
		return fmt.Errorf("%s", reply.Str)
	}
	for _, command := range commands {
		reply, err := client.Do(command...)
		if err != nil {
			return err
		}
		if reply.Type == redis.ErrorReply {
			return fmt.Errorf("setup %v: %s", command, reply.Str)
		}
	}
	return nil
}

func (item Case) Reference(client *Client) (redis.Reply, error) {
	reply, _, err := item.ReferenceWire(client)
	return reply, err
}

func (item Case) ReferenceWire(client *Client) (redis.Reply, []byte, error) {
	if len(item.Request) > 0 {
		argv := make([][]byte, len(item.Request))
		for i, arg := range item.Request {
			argv[i] = []byte(arg)
		}
		return client.CommandWire(argv)
	}
	command := "EVAL"
	if item.ReadOnly {
		command = "EVAL_RO"
	}
	arguments := []string{command, item.Source, strconv.Itoa(len(item.Keys))}
	arguments = append(arguments, item.Keys...)
	arguments = append(arguments, item.Args...)
	argv := make([][]byte, len(arguments))
	for i, arg := range arguments {
		argv[i] = []byte(arg)
	}
	return client.CommandWire(argv)
}

func (item Case) Execute(engine *redis.Engine, host redis.Host) redis.Reply {
	if len(item.Request) > 0 {
		argv := make([][]byte, len(item.Request))
		for i, arg := range item.Request {
			argv[i] = []byte(arg)
		}
		return engine.Command(argv, redis.Run{Host: host, ReadOnly: item.ReadOnly, Proto: item.Proto})
	}
	keys, args := make([][]byte, len(item.Keys)), make([][]byte, len(item.Args))
	for i, key := range item.Keys {
		keys[i] = []byte(key)
	}
	for i, arg := range item.Args {
		args[i] = []byte(arg)
	}
	return engine.Eval([]byte(item.Source), keys, args, redis.Run{Host: host, ReadOnly: item.ReadOnly, Proto: item.Proto})
}
