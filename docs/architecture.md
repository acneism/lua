# Architecture

## A0: reference infrastructure

`testdata/snippets.json` stores the supplied Redis 7.2.16 corpus.
`internal/cmd/capture` reads source inputs, checks the reference server version,
executes the capture wrapper over RESP2, and captures or verifies answers.

The RESP client is private to this command. It only handles the bulk and error
replies that `INFO server` and the wrapper need. It limits reply sizes and request
duration. It is not the public `redis.Reply` parser planned for stage B.

## Planned implementation

1. A1: interpreter core, base library, coroutine library, and execution ADRs.
2. A2: string, table, and math libraries.
3. B: Redis environment, host interface, replies, EVAL, and SCRIPT.
4. C: bit, struct, cjson, and cmsgpack libraries.
5. D: FUNCTION, FCALL, and Redis DUMP/RESTORE compatibility.
6. E: resource limits, interruption, and final performance measurements.

The root package will contain the interpreter. The `redis` package will contain
the Redis environment and engine. Internal packages will hold the test host,
LZF, and Redis CRC64. Execution strategy, value representation, table layout,
and coroutine decisions belong in ADRs with evidence from the relevant stage.
