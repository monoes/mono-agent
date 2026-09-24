package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func refOrgCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "org",
		Short: "Orgs, automations, grants, automation roles, autonomy, and holding orgs",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print(`
╔══════════════════════════════════════════════════════════════╗
║        monoagentcli — Orgs and automations (org …)           ║
╚══════════════════════════════════════════════════════════════╝

An org is a team of agent roles run by monomind. Its config is
<profile folder>/.monomind/orgs/<name>.json; org commands use the active
profile's folder unless --project is given. Every org command prints JSON.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
PROCESSES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  monoagentcli org serve            start monomind's org daemon for the folder
  monoagentcli org serve --stop     stop the folder's running orgs and daemon
  monoagentcli daemon               engine + HTTP API + automation-role endpoint
                                    + autonomy decisions (writes a heartbeat)
  monoagentcli --json status        both daemons' state

  Without the daemon: granted tools return daemon_required, automation
  roles cannot run, and every org behaves as autonomy level manual.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
AUTOMATIONS AND GRANTS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  org automation list|unassigned
  org automation add <org> --workflow <id> --alias <alias> [--owned]
  org automation remove <org> --alias <alias>

  org grant list <org> [--role <id>]
  org grant add <org> --role <id> --automation <alias>
      [--mode run|trigger|status] [--wait=true|false] [--timeout 600]
      [--approval none|required] [--max-calls-per-run 20]
      [--max-calls-per-day 200] [--max-output-bytes 16384]
  org grant remove <org> --role <id> --automation <alias>
  org effective-tools <org> --role <id>

  A grant gives the role the tool monoagent__automation_<alias>. Aliases:
  a lowercase letter, then letters, digits, or underscores; not human,
  workflow, status, or output. Workflows with outbound nodes default to
  --approval required (each call is a decision). The first grant of a
  role pre-fills denyTools Bash.

  The tool's arguments reach the workflow as input in its trigger data.
  monomind passes only the arguments a tool's schema lists, so the tool
  lists the input fields the workflow's templates read (input.<field>);
  a workflow that reads its input some other way (a code node) needs an
  input_schema on the role's automations entry in the org file.

  A granted run gets the calling role's workdir (its run_config.workspace
  directory) as org.workdir in its trigger data; so does an automation
  role's run started by a message from a role. File nodes (spreadsheet,
  write file, image, attachments, uploads, FTP local paths, browser
  uploads) then refuse any path that resolves outside it, after following
  .. and symlinks; relative paths are taken from the workdir. Shell
  commands cannot be confined. automation list, grant add/list and
  effective-tools report file_input_nodes: nodes whose paths come from
  the run's input.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
AUTOMATION ROLES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  org automation-role add <org> --alias <alias> --reports-to <role>
      [--title T] [--reply last_node|node:<name>]
  org automation-role remove <org> --role <id>
  org automation-role rotate <org> --role <id>

  Messages to the role start the workflow (trigger data: org_message,
  text, trace); when the run ends its output is sent back to the sender
  as "re: <subject>".

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
WORKFLOW NODES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  org.run      start an org run and wait (wait:false returns at once;
               exclusive:true refuses to join a run it did not start)
  org.send     message a role; output says delivery live or queued
  org.ask      ask a role and pause until it replies (the workflow must
               be an automation role of that org)
  trigger.org  start on org events (event_types, role, subject_match,
               question_kind ask_human|approval|any) or on messages to
               this workflow's automation role (mode endpoint)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
MESSAGES AND LIFECYCLE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  org send <org> --to <role> [--from org:role] --subject S --body B
  org queued <org>                  messages waiting for the org's next start
                                    (read-only view of monomind's inbox.jsonl)
  org stop|pause|resume <org>
  org rename <org> <new>            org names are unique on this machine
  org delete <org> [--force]
  org legacy list | legacy move <org>
  org reconcile                     rewrite every org file's generated blocks
                                    from the grant rows (after a folder move)
  org teardown-profile [--dry-run]  the org half of deleting a profile: revoke
                                    its grants, endpoints, autonomy; stop its
                                    orgs and org daemon

  Crossings carry "[trace chn_… hop=N]" and stop at run_config.max_hops
  (default 8) or max_repeats calls to one target per minute (default 20).
  Granted calls on one chain add one hop per 8, whichever roles make them.
  Org-started runs continue a chain; a webhook run continues one only from
  a signed X-Monoagent-Trace header (sent by http.request), else it starts
  fresh. A loop through a webhook is refused (HTTP 429) at the hop limit
  of the org the chain started in; the token expires after an hour.
  Webhook and trigger.org runs are limited to 200 per workflow per minute
  (HTTP 429 for a webhook).

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
AUTONOMY
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  org autonomy show <org>
  org autonomy set <org> [--level manual|mid|full]
      [--decider model|boss|parent] [--decider-model M] [--decider-runtime R]
      [--decider-timeout 120] [--policy TEXT | --policy-file F]
      [--tier <class>=routine|consequential|irreversible]... [--clear-tier C]...
      [--on-decider-failure deny|human] [--max-decisions N] [--max-decider-usd X]
  org autonomy pause <org>|--all [--for 30m]     resume <org>|--all
  org autonomy decisions <org> [--run R] [--verdict V] [--limit N]
  org autonomy needs-you <org>

                  routine        consequential   irreversible
    manual        person         person          person
    mid           rule approves  decider         person
    full          rule approves  decider         decider

  Classes and default tiers: tool:<Name> routine (tool:Bash consequential
  for a role with grants and Bash allowed), org_complete, question, and
  org_start consequential, gate irreversible, grant:<alias> and
  hil:<alias> by the workflow (irreversible with outbound nodes).
  Wildcards tool:* and grant:* are accepted.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
HOLDING ORGS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  "kind": "holding", "children": [{"org":"sales","start":"on_demand",
  "budget_share":0.5}], run_config.group_budget_usd

  org group init <holding>          Initiator tools + report-up lines
  org group start|stop|status <holding>

  A child belongs to at most one holding org, in the same profile.

  A message to a stopped child starts a full run of the child's own goal,
  so phrase child goals for messages: "Handle requests from hq; when idle,
  complete." and use org_start(org, task) for task-specific runs.
  validate, create-json, and group init add a non-fatal entry to
  "warnings" for a child goal that lacks a request word (request, message,
  ask, inquiry, respond, reply) or lacks the parent's name, idle, or
  complete. run_config.idle_minutes (default 10) keeps a woken child short.

`)
		},
	}
}
