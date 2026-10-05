# Yousage

A Go project for exploring AI coding-provider usage monitoring.

## Development

The project currently uses Go 1.26.2 and the standard library only. The local
module name is `yousage`; a remote repository module path can be set when the
project's repository location is established.

```powershell
go run .
go test ./...
go vet ./...
go build -o "$env:TEMP\yousage.exe" .
```

The executable currently prints an initialization message. Provider collectors
and usage reporting are not implemented yet.

## Research

See [the t3code usage implementation notes](docs/t3code-usage.md) for inspected
data sources, refresh behavior, and possible directions for this project.
