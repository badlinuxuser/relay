# Static checks for sessions without a Go toolchain

The development sandbox of Claude has no Go and no network, so code is written without a compiler. These scripts catch
the commonest mistakes before the author compiles. They are heuristics (they can be wrong: three backticks inside a
Go string confuse `imports.py`), and they never replace `go vet ./... && go build ./... && go test ./...`.

    python3 tools/check/balance.py $(find cmd internal -name '*.go')   # brackets are balanced
    python3 tools/check/imports.py $(find cmd internal -name '*.go')   # unused / missing imports (local variables named fs are reported: ignore)
    python3 tools/check/dups.py                                        # the same name declared twice in one package (this found `skillArgs`)

The Python SDK of the builder has a real test: `python3 internal/builder/sdk/test_relay_mcp.py`.
