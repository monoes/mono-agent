#!/usr/bin/env python3
# Scripted stand-in for the codex CLI: no model. Emits codex exec --json events
# and ```tool_call fences so monomind's REAL runtime drives org_send / org_doc_*.
import sys, os, re, json
args = sys.argv[1:]
prompt = sys.stdin.read()
state = os.environ["FAKE_STATE"]; os.makedirs(state, exist_ok=True)
thread = None
if "resume" in args:
    thread = args[args.index("resume") + 1]
m = re.search(r'You are agent "([\w-]+)"', prompt)
role = (thread or "").split("--")[1] if thread and "--" in thread else (m.group(1) if m else "unknown")
thread = thread or f"fake--{role}--1"
cf = os.path.join(state, role); n = int(open(cf).read()) + 1 if os.path.exists(cf) else 1
open(cf, "w").write(str(n))
open(os.path.join(state, f"{role}.{n}.prompt"), "w").write(prompt)
def call(name, a): return "```tool_call\n" + json.dumps({"name": name, "arguments": a}) + "\n```"
text = "ok"
tr = prompt.split("Tool results:")[-1] if "Tool results:" in prompt else ""
last = re.findall(r'"name":"(\w+)"', tr)[-1] if tr else ""
idf = os.path.join(state, "docid")
def docid():
    return open(idf).read() if os.path.exists(idf) else ""
if role == "lead":
    if n == 1: text = "Delegating.\n" + call("org_send", {"to": "writer", "subject": "task", "message": "write the note"})
    elif re.search(r"accept", prompt) and "[message" in prompt or os.path.exists(os.path.join(state, "decided")) and last != "org_complete":
        text = call("org_complete", {"outcome": "achieved", "summary": "note written and accepted"})
elif role == "writer":
    if last == "": text = call("org_doc_publish", {"type": "note", "body": {"text": "hello from the scripted writer"}})
elif role == "checker":
    mm = re.search(r'document "([\w-]+)"', prompt)
    if last == "" and mm and not os.path.exists(os.path.join(state, "decided")):
        open(idf, "w").write(mm.group(1)); text = call("org_doc_read", {"id": mm.group(1)})
    elif last == "org_doc_read":
        import time; time.sleep(float(os.environ.get("FAKE_SLEEP","0")))
        text = call("org_doc_decide", {"id": docid(), "version": 1, "decision": "accept"})
    elif last == "org_doc_decide":
        open(os.path.join(state, "decided"), "w").write("1")
        text = call("org_send", {"to": "lead", "subject": "review", "message": "note accepted"})
sys.stdout.write(json.dumps({"type": "thread.started", "thread_id": thread}) + "\n")
sys.stdout.write(json.dumps({"type": "item.completed", "item": {"id": "i1", "type": "agent_message", "text": text}}) + "\n")
sys.stdout.write(json.dumps({"type": "turn.completed", "usage": {"input_tokens": 10, "output_tokens": 5, "cached_input_tokens": 0}}) + "\n")
