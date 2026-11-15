# AC-C03: 关闭后操作

- **切片**: S-01.
- **测试**: TestClosedOperations
- **命令**: `go test -race -v -run '^TestClosedOperations$' .`
- **原始输出**:

```text
=== RUN   TestClosedOperations
--- PASS: TestClosedOperations (0.00s)
PASS
ok  	github.com/xogas/wayland	1.009s
```

- **结论**: PASS; Dispatch / Sync / GetRegistry / Roundtrip 均返回 ErrClosed; 重复 Close 为 nil.
