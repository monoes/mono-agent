#!/usr/bin/env python3
"""A fake `codex` for tests that run the REAL monomind with no model call.

Point CODEX_CLI_BIN at this file and monomind's codex runner drives it exactly as it
drives codex: one process per `codex exec`, the prompt on stdin, JSON events on
stdout, and monomind's own tool protocol (a fenced tool_call block in the
assistant's text, answered by a tool_result block in the next prompt). A resumed
turn is `codex exec ... resume <thread>`.

What it does is set by the prompt, so a replay (a new thread whose prompt carries
the result) behaves like a resume:
  - a prompt that carries no tool result gets a short word and one tool call,
    get_weather for Paris (FAKE_CODEX_MODE=parallel: Paris and Tokyo, in the same
    message; FAKE_CODEX_MODE=text: no call, just an answer);
  - a prompt that carries a result gets "FINAL: temps seen N".
Env:
  FAKE_CODEX_LOG    a file that gets one JSON line per process (pid, argv, prompt)
  FAKE_CODEX_STATE  a folder where it counts the rounds of each thread
  FAKE_CODEX_TOOL   the function it calls (default get_weather)
  FAKE_CODEX_ARGS   the arguments of the call it makes (a JSON object, default {"city": "Paris"})
"""
import json
import os
import re
import sys

args = sys.argv[1:]
prompt = sys.stdin.read()
mode = os.environ.get("FAKE_CODEX_MODE", "single")
tool = os.environ.get("FAKE_CODEX_TOOL", "get_weather")
call_args = json.loads(os.environ.get("FAKE_CODEX_ARGS") or '{"city": "Paris"}')
state = os.environ.get("FAKE_CODEX_STATE", "/tmp")
os.makedirs(state, exist_ok=True)

thread = args[args.index("resume") + 1] if "resume" in args else "th_fake_%d" % os.getpid()
counter = os.path.join(state, "round_" + thread)
if "resume" in args and not os.path.exists(counter):
    # What codex does with a thread it does not know.
    sys.stderr.write("Error: thread/resume: thread/resume failed: no rollout found for thread id %s (code -32600)\n" % thread)
    sys.exit(1)
rnd = int(open(counter).read()) if os.path.exists(counter) else 0
open(counter, "w").write(str(rnd + 1))

log = os.environ.get("FAKE_CODEX_LOG")
if log:
    with open(log, "a") as f:
        f.write(json.dumps({"pid": os.getpid(), "round": rnd, "argv": args, "prompt": prompt}) + "\n")


def emit(o):
    sys.stdout.write(json.dumps(o) + "\n")
    sys.stdout.flush()


def fence(name, arguments):
    return "```tool_call\n" + json.dumps({"name": name, "arguments": arguments}) + "\n```"


emit({"type": "thread.started", "thread_id": thread})
emit({"type": "turn.started"})

# The prompt of a resumed turn is the caller's text; the one of a first turn starts
# with monomind's tool protocol. Either way a result shows as a temperature.
temps = re.findall(r"temp_c[^0-9-]*(-?\d+)", prompt)
if mode == "text":
    text = "plain answer, no tool"
elif temps:
    text = "FINAL: temps seen %s" % ",".join(temps)
elif mode == "parallel":
    text = "looking both up\n" + fence(tool, {"city": "Paris"}) + "\n" + fence(tool, {"city": "Tokyo"})
else:
    text = "checking\n" + fence(tool, call_args)

emit({"type": "item.completed", "item": {"id": "item_0", "type": "agent_message", "text": text}})
emit({"type": "turn.completed", "usage": {"input_tokens": 1000 + len(prompt) // 4, "cached_input_tokens": 0, "output_tokens": 30}})
