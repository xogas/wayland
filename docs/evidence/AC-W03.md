# AC-W03: 格式错误

- **切片**: S-01.
- **测试**: TestMalformedDecode
- **命令**: `go test -race -v -run '^TestMalformedDecode$' .`
- **原始输出**:

```text
=== RUN   TestMalformedDecode
--- PASS: TestMalformedDecode (0.00s)
PASS
ok  	github.com/xogas/wayland	1.008s
```

- **结论**: PASS; 缺 NUL 与尾随字节均返回 ErrMalformedMessage.
