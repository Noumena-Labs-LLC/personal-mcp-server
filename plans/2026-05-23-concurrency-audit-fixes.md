Context

The concurrency audit found three concrete risks that should be fixed in the
next pass:

- `liveHandler.ServeHTTP` can leak `inFlight` if the delegated handler panics,
  which can stall shutdown.
- `approval.Manager.Request` prints before starting its timeout timer, so a
  blocked stdout can bypass the configured approval timeout.
- `jobOutputStream.CloseAndFlush` blocks forever waiting for the consumer
  goroutine, which can hang background-job shutdown if the consumer stalls.

Status

Completed.

Approach

- Patch `cmd/personal-mcp-server/runtime_lifecycle.go` so request accounting is
  released with `defer` and cannot leak on panic.
- Rework `internal/approval/approval.go` so timeout accounting starts before
  any potentially blocking logging or request setup, and add regression tests
  for the timeout path.
- Add a bounded shutdown path for `internal/shell/job_output_stream.go` so
  `CloseAndFlush` cannot hang forever if the consumer stops making progress.
- Add focused tests for the panic/shutdown path, approval timeout ordering, and
  the job output stream close path.

Verification

- Run focused tests for `cmd/personal-mcp-server`, `internal/approval`, and
  `internal/shell`.
- Run `just vet` as the repo-wide verification pass available in this checkout.

Completed:

- Added `defer state.release()` in `cmd/personal-mcp-server/runtime_lifecycle.go`
  so handler panics cannot leak `inFlight`.
- Moved approval timeout accounting ahead of the asynchronous approval log
  write in `internal/approval/approval.go`.
- Switched `internal/shell/job_output_stream.go` to a bounded close path that
  closes the channel and waits briefly for the consumer to finish flushing.
- Added regression tests for the panic path, approval timeout ordering, and
  bounded job output stream shutdown.
- Ran focused `go test` runs for the three affected packages and `just vet`.
