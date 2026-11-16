# wayland

Pure-Go Wayland client library for linux/amd64. Standard library only; no CGO.

## Commands

```sh
make generate
make test
go test -race -count=1 -tags=integration ./...
go run ./examples/registry
go run ./cmd/wayland-info
```

See [doc.go](doc.go) for the package and file layout. The programs under
`examples/` are standalone: `shm-surface` and `xdg-shell` are the minimal
starting points, `imageviewer` takes an optional image path, and `dmabuf` needs
a DRM node plus compositor linux-dmabuf support.

## License

MIT.
