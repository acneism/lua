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
		if strings.HasPrefix(name, "evalsha") && len(argv[1]) != 40 {
			return Error("NOSCRIPT No matching script. Please use EVAL.")
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
	case "fcall", "fcall_ro":
		if len(argv) < 3 {
			return wrong(name)
		}
		e.functions.mu.RLock()
		known := e.functions.current.functions[string(argv[1])] != nil
		e.functions.mu.RUnlock()
		if !known {
			return Error("ERR Function not found")
		}
		count, ok := parseKeyCount(string(argv[2]))
		if !ok {
			return Error("ERR Bad number of keys provided")
		}
		if count > int64(len(argv)-3) {
			return Error("ERR Number of keys can't be greater than number of args")
		}
		if count < 0 {
			return Error("ERR Number of keys can't be negative")
		}
		run.ReadOnly = run.ReadOnly || name == "fcall_ro"
		return e.fcall(string(argv[1]), argv[3:3+count], argv[3+count:], run, argv)
	case "function":
		if len(argv) < 2 {
			return wrong(name)
		}
		return e.functionCommand(argv, wrong)
	}
	return Error("ERR unknown scripting command '" + name + "'")
}

func (e *Engine) functionCommand(argv [][]byte, wrong func(string) Reply) Reply {
	syntax := func() Reply {
		return Error("ERR unknown subcommand or wrong number of arguments for '" + string(argv[1]) + "'. Try FUNCTION HELP.")
	}
	switch sub := strings.ToLower(string(argv[1])); sub {
	case "load":
		if len(argv) < 3 {
			return wrong("function|load")
		}
		replace := false
		for _, option := range argv[2 : len(argv)-1] {
			if !strings.EqualFold(string(option), "replace") {
				return Error("ERR Unknown option given: " + cstring(string(option)))
			}
			replace = true
		}
		return e.FunctionLoad(argv[len(argv)-1], replace)
	case "delete":
		if len(argv) != 3 {
			return wrong("function|delete")
		}
		return e.FunctionDelete(string(argv[2]))
	case "list":
		withCode, filter, pattern := false, false, ""
		for index := 2; index < len(argv); index++ {
			option := string(argv[index])
			switch {
			case !withCode && strings.EqualFold(option, "withcode"):
				withCode = true
			case !filter && strings.EqualFold(option, "libraryname"):
				if index >= len(argv)-1 {
					return Error("ERR library name argument was not given")
				}
				index++
				pattern, filter = string(argv[index]), true
			default:
				return Error("ERR Unknown argument " + option)
			}
		}
		return e.functionList(pattern, filter, withCode)
	case "stats":
		if len(argv) != 2 {
			return wrong("function|stats")
		}
		return e.FunctionStats()
	case "dump":
		if len(argv) != 2 {
			return wrong("function|dump")
		}
		return Bulk(string(e.FunctionDump()))
	case "restore":
		if len(argv) < 3 {
			return wrong("function|restore")
		}
		if len(argv) > 4 {
			return syntax()
		}
		policy := ""
		if len(argv) == 4 {
			policy = string(argv[3])
			if policy == "" {
				policy = "?"
			}
		}
		return e.FunctionRestore(argv[2], policy)
	case "flush":
		if len(argv) > 3 {
			return syntax()
		}
		if len(argv) == 3 && !strings.EqualFold(string(argv[2]), "sync") && !strings.EqualFold(string(argv[2]), "async") {
			return Error("ERR FUNCTION FLUSH only supports SYNC|ASYNC option")
		}
		e.FunctionFlush()
		return Status("OK")
	case "kill":
		if len(argv) != 2 {
			return wrong("function|kill")
		}
		return e.Kill(Function)
	}
	return Error("ERR unknown subcommand '" + string(argv[1]) + "'. Try FUNCTION HELP.")
}
