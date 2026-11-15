# AC-W04: fd 编解码

- **切片**: S-01.
- **测试**: TestWireRoundTrip
- **命令**: `go test -race -v -run '^TestWireRoundTrip$' .`
- **原始输出**:

```text
=== RUN   TestWireRoundTrip
--- PASS: TestWireRoundTrip (0.00s)
PASS
ok  	github.com/xogas/wayland	1.006s
```

- **结论**: PASS; fd 进入 ancillary, 解码后的 fd 与发送 fd 相同.
