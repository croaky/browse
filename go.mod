module github.com/croaky/browse

go 1.27.3

require github.com/croaky/is v0.2.0

require (
	golang.org/x/mod v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/telemetry v0.0.0-20260508192327-42602be52be6 // indirect
	golang.org/x/tools v0.45.0 // indirect
)

tool (
	golang.org/x/tools/cmd/deadcode
	golang.org/x/tools/cmd/goimports
)
