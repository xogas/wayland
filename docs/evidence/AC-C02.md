# AC-C02: 缺失来源

- **切片**: S-01.
- **测试**: TestConnectMissingSource
- **命令**: `go test -race -v -run '^TestConnectMissingSource$' .`
- **原始输出**:

```text
=== RUN   TestConnectMissingSource
--- PASS: TestConnectMissingSource (0.00s)
PASS
ok  	github.com/xogas/wayland	1.010s
```

- **结论**: PASS; 清空 WAYLAND_SOCKET / WAYLAND_DISPLAY / XDG_RUNTIME_DIR 后返回 ErrSource.
