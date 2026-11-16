# wayland

Pure-Go Wayland client library for linux/amd64. Standard library only; no CGO.

## Commands

```sh
make generate
make test
go test -race -count=1 -tags=integration ./...
go run ./examples/registry
```

See [doc.go](doc.go) for the package and file layout.

## License

MIT.
