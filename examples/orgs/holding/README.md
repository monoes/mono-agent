# Holding org: hq with sales and support

`hq` is a holding org. Its CEO starts `sales` or `support` with the
`org_start` tool, each child's boss reports back to `hq:ceo`, and the group
stops spending at `run_config.group_budget_usd` ($10, half of it per child).
The children decide their own approvals through hq's CEO (`--decider
parent`).

Needs monomind with capability `org-tool-providers`. Org names are unique
on a machine; rename the files' `name` fields if you already have these.

```bash
for o in sales support hq; do
  monoagentcli org create-json "$o" --json "$(cat examples/orgs/holding/$o.json)"
done

# CEO gets org_start/org_stop/org_status/org_report over the children, and
# each child's boss gets a report-up line.
monoagentcli org group init hq

# hq decides for its children; starting a child is a decision for hq's own
# decider (consequential: the model at mid).
monoagentcli org autonomy set sales --decider parent
monoagentcli org autonomy set support --decider parent

monoagentcli daemon &
monoagentcli org serve
monoagentcli org group start hq --task "A customer asks why their invoice doubled this month."
monoagentcli org group status hq      # children, status, cost roll-up
monoagentcli org autonomy decisions hq
```

If a child's run ends without its boss reporting up, the daemon sends the
child's run report to `hq:ceo` itself.
