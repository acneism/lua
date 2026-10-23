import socket
import subprocess
import time

scripts = {
    "Loop": "local s=0 for i=1,10000000 do s=s+i end return s",
    "Fib27": "local function fib(n) if n<2 then return n end return fib(n-1)+fib(n-2) end return fib(27)",
    "LuaCalls": "local function f(x) return x+1 end local n=0 for i=1,10000 do n=f(n) end return n",
    "TableInsert": "local t={} for i=1,1000000 do t[#t+1]=i end return #t",
    "TableHash": "local t={} for i=1,100000 do t['k'..i]=i end local n=0 for k,v in pairs(t) do n=n+v end return n",
    "Sort": "local t={} for i=1,100000 do t[i]=100001-i end table.sort(t) return t[1]+t[100000]",
    "GSub": "return #string.gsub(string.rep('abc ',262144),'%a+','x')",
    "StringConcat": "local t={} for i=1,100000 do t[i]=tostring(i) end return #table.concat(t)",
    "Metamethod": "local t=setmetatable({}, {__add=function(a,b) return b+1 end}) local n=0 for i=1,10000 do n=t+n end return n",
}

connection = socket.create_connection(("127.0.0.1", 6390))
connection.settimeout(30)
reader = connection.makefile("rb")


def command(*args):
    values = [str(arg).encode() for arg in args]
    request = b"*" + str(len(values)).encode() + b"\r\n"
    for value in values:
        request += b"$" + str(len(value)).encode() + b"\r\n" + value + b"\r\n"
    connection.sendall(request)
    header = reader.readline()
    if header[:1] == b":":
        return int(header[1:])
    if header[:1] == b"$":
        size = int(header[1:])
        value = reader.read(size)
        assert reader.read(2) == b"\r\n"
        return value
    raise RuntimeError(header)


assert b"redis_version:7.2.16\r\n" in command("INFO", "server")
for repeat in range(3):
    print(f"ROUND {repeat + 1}", flush=True)
    for name, script in scripts.items():
        command("EVAL", script, 0)
        started = time.monotonic_ns()
        answer = command("EVAL", script, 0)
        elapsed = time.monotonic_ns() - started
        print(f"REDIS {name} {elapsed} ns answer={answer}", flush=True)
    subprocess.run(
        ["go", "test", ".", "-run", "^$", "-bench",
         "^Benchmark(Loop|Fib27|LuaCalls|TableInsert|TableHash|Sort|GSub|StringConcat|Metamethod)$",
         "-benchtime=1x", "-count=1", "-benchmem"], check=True)
subprocess.run(["go", "test", ".", "-run", "^$", "-bench",
                "^BenchmarkNumberValue$", "-benchtime=100ms",
                "-count=3", "-benchmem"], check=True)
connection.close()
