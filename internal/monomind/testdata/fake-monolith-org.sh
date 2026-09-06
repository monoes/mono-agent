#!/bin/sh
# Fake monomind for OrgRun's process-group kill test (TestOrgRunCancelGroupKill):
# answers the version handshake normally (Ensure() needs this to succeed
# before OrgRun even starts its own subprocess), then for any other
# invocation (the `org run ...` call itself) spawns a long-lived grandchild
# and hangs forever. Only a group kill can reap both processes.
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"2.10.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'
  exit 0
fi
sleep 60 &
CHILD=$!
wait $CHILD
