#!/usr/bin/env python3
# Scripted stand-in for the grok CLI: no model. Speaks grok's streaming-messages-json
# (assistant text + a result with usage and total_cost_usd) so monomind's REAL grok
# runner and runtime drive org_send / org_doc_* and meter a real cost per invocation.
# FAKE_COSTS is a JSON object {role: usd per invocation}.
import sys, os, re, json
args = sys.argv[1:]
prompt = args[args.index("-p") + 1]
state = os.environ["FAKE_STATE"]; os.makedirs(state, exist_ok=True)
costs = json.loads(os.environ.get("FAKE_COSTS", "{}"))
thread = args[args.index("--resume") + 1] if "--resume" in args else None
m = re.search(r'You are agent "([\w-]+)"', prompt)
role = (thread or "").split("--")[1] if thread and "--" in thread else (m.group(1) if m else "unknown")
thread = thread or f"fake--{role}--1"
cf = os.path.join(state, role); n = int(open(cf).read()) + 1 if os.path.exists(cf) else 1
open(cf, "w").write(str(n))
def call(name, a): return "```tool_call\n" + json.dumps({"name": name, "arguments": a}) + "\n```"
text = "ok"
tr = prompt.split("Tool results:")[-1] if "Tool results:" in prompt else ""
last = re.findall(r'"name":"(\w+)"', tr)[-1] if tr else ""
idf = os.path.join(state, "docid")
def docid():
    return open(idf).read() if os.path.exists(idf) else ""
pub = os.path.join(state, "published")
if role == "lead":
    if n == 1:
        text = "Delegating.\n" + "\n".join(call("org_send", {"to": "writer", "subject": s, "message": s}) for s in ("write the note", "polish the note", "polish the note once more"))
    elif (re.search(r"accept", prompt) and "[message" in prompt or os.path.exists(os.path.join(state, "decided"))) and last != "org_complete":
        text = call("org_complete", {"outcome": "achieved", "summary": "note written and accepted"})
elif role == "writer":
    if last == "" and not os.path.exists(pub):
        open(pub, "w").write("1"); text = call("org_doc_publish", {"type": "note", "body": {"text": "hello from the scripted writer"}})
elif role == "checker":
    mm = re.search(r'document "([\w-]+)"', prompt)
    if last == "" and mm and not os.path.exists(os.path.join(state, "decided")):
        open(idf, "w").write(mm.group(1)); text = call("org_doc_read", {"id": mm.group(1)})
    elif last == "org_doc_read":
        text = call("org_doc_decide", {"id": docid(), "version": 1, "decision": "accept"})
    elif last == "org_doc_decide":
        open(os.path.join(state, "decided"), "w").write("1")
        text = call("org_send", {"to": "lead", "subject": "review", "message": "note accepted"})
w = lambda o: sys.stdout.write(json.dumps(o) + "\n")
w({"type": "system", "session_id": thread})
w({"type": "assistant", "session_id": thread, "message": {"content": [{"type": "text", "text": text}]}})
w({"type": "result", "subtype": "success", "session_id": thread, "usage": {"input_tokens": 1000, "output_tokens": 500}, "total_cost_usd": costs.get(role, 0)})
