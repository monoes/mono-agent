# Benchmarks

Performance baseline for the engine hot paths covered by the benchmark
suite: expression/template evaluation, per-node item throughput, workflow
store save/load, and secret/output redaction.

## Running

```bash
make bench          # go test -bench=. -benchtime=3s -count=5 -run=^$ ...  -> bench.txt
benchstat bench.txt  # go install golang.org/x/perf/cmd/benchstat@latest
```

A weekly scheduled job (`.github/workflows/bench.yml`, Monday 06:00 UTC, plus
manual `workflow_dispatch`) runs the same target on CI and uploads
`bench.txt` + the benchstat summary as a build artifact. **No per-PR
gating and no alert threshold exist yet** — that's a deliberate follow-up
decided after two scheduled CI baseline runs exist to compare against
(issue #28), not missing work.

## Dev-machine baseline (2026-09-01)

**These numbers come from a local development machine, not the CI
runner.** They establish that the benchmarks run and produce sane numbers,
and give a rough feel for relative cost — they are not the reference
baseline for regression detection. The first scheduled `bench.yml` run
against `ubuntu-latest` will establish that.

- Go: `go1.26.6 darwin/arm64`
- CPU: Apple M2 (8 logical cores)
- Command: `make bench` (`-benchtime=3s -count=5`)
- Full raw output and benchstat formatting below are from a single local
  run; treat them as illustrative, not authoritative — this machine was
  also running other concurrent Go builds/tests during capture, so a couple
  of benchmarks (`WorkflowFileStore_List`, `RedactItems_1000Items`,
  `CodeNode_1000Items`) show more run-to-run variance than expected on an
  idle machine.

### internal/workflow

| Benchmark | sec/op |
|---|---|
| `ExpressionEngine_Simple` | 6.82 µs |
| `ExpressionEngine_Nested` | 9.00 µs |
| `ExpressionEngine_FallbackChain` | 19.43 µs |
| `WorkflowFileStore_List` (cached) | 2.69 ms |
| `WorkflowFileStore_ListNoCache` | 27.05 ms |
| `RedactItems_1000Items` | 29.49 ms |
| `WorkflowFileStore_SaveLargeWorkflow` (~100 nodes / ~200 edges) | 4.65 ms |
| `WorkflowFileStore_LoadLargeWorkflow` (~100 nodes / ~200 edges) | 2.73 ms |

### internal/nodes/control

| Benchmark | sec/op |
|---|---|
| `SetNode_1000Items` | 8.92 ms |
| `FilterNode_1000Items` | 16.82 ms |
| `CodeNode_1000Items` (goja) | 13.24 ms |

(`sec/op` values above are benchstat's median-of-5 formatting; see
`bench.txt` in a CI artifact for the full `-count=5` sample set used to
compute them.)

## What's covered

- **Expression evaluation** (`internal/workflow/expression_bench_test.go`):
  a simple `{{ $json.field }}` lookup, a nested `{{ $json.a.b.c }}` lookup,
  and a longer fallback-chain expression combining a conditional with a
  `$node[...]` lookup.
- **Per-node item throughput** (`internal/nodes/control/nodes_bench_test.go`):
  `core.set`, `core.filter`, and `core.code` each run against a batch of
  1000 items.
- **Workflow store save/load**
  (`internal/workflow/storage_bench_test.go`): saving and loading a
  workflow with ~100 nodes and ~200 edges via the JSON file store.
- **Redaction** (`internal/workflow/redact_bench_test.go`): `RedactItems`
  over a batch of 1000 items with a mix of credential-shaped and normal
  fields.
