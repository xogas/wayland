# AC-W01: 参数编解码

- **切片**: S-01.
- **测试**: TestWireRoundTrip, TestStringNullAndEmpty
- **命令**: `go test -race -v -run '^(TestWireRoundTrip|TestStringNullAndEmpty)$' .`
- **原始输出**:

```text
=== RUN   TestWireRoundTrip
--- PASS: TestWireRoundTrip (0.00s)
=== RUN   TestStringNullAndEmpty
--- PASS: TestStringNullAndEmpty (0.00s)
PASS
ok  	github.com/xogas/wayland	1.008s
```

- **结论**: PASS; int / uint / fixed / string / array / null string / null array 往返一致.
