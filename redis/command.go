package redis

import "strings"

func (e *Engine) Command(argv [][]byte, run Run) Reply {
	if len(argv) == 0 {
		return Error("ERR missing command")
	}
	name := strings.ToLower(string(argv[0]))
	wrong := func(name string) Reply { return Error("ERR wrong number of arguments for '" + name + "' command") }
	switch name {
	case "eval", "eval_ro", "evalsha", "evalsha_ro":
		if len(argv) < 3 {
			return wrong(name)
		}
		if strings.HasPrefix(name, "evalsha") {
			e.mu.Lock()
			found := e.scripts[strings.ToLower(string(argv[1]))] != nil
			e.mu.Unlock()
			if !found {
				return Error("NOSCRIPT No matching script. Please use EVAL.")
			}
		}
		keys, args, failure := ParseNumKeys(string(argv[2]), argv[3:])
		if failure != nil {
			return *failure
		}
		run.ReadOnly = run.ReadOnly || strings.HasSuffix(name, "_ro")
		if strings.HasPrefix(name, "evalsha") {
			return e.EvalSHA(string(argv[1]), keys, args, run)
		}
		return e.Eval(argv[1], keys, args, run)
	case "script":
		if len(argv) < 2 {
			return wrong(name)
		}
		sub := strings.ToLower(string(argv[1]))
		switch sub {
		case "load":
			if len(argv) != 3 {
				return wrong("script|load")
			}
			return e.ScriptLoad(argv[2])
		case "exists":
			if len(argv) < 3 {
				return wrong("script|exists")
			}
			shas := make([]string, len(argv)-2)
			for i, arg := range argv[2:] {
				shas[i] = string(arg)
			}
			return e.ScriptExists(shas...)
		case "kill":
			if len(argv) != 2 {
				return wrong("script|kill")
			}
			return e.Kill(Script)
		case "flush":
			if len(argv) == 2 || len(argv) == 3 && (strings.EqualFold(string(argv[2]), "sync") || strings.EqualFold(string(argv[2]), "async")) {
				e.ScriptFlush()
				return Status("OK")
			}
			return Error("ERR SCRIPT FLUSH only support SYNC|ASYNC option")
		}
		return Error("ERR unknown subcommand '" + string(argv[1]) + "'. Try SCRIPT HELP.")
	}
	return Error("ERR unknown scripting command '" + name + "'")
}
