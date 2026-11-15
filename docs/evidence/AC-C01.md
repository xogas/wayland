# AC-C01: 连接与关闭

- **切片**: S-01.
- **测试**: TestConnectSway
- **命令**: `go test -race -v -run '^TestConnectSway$' .`
- **原始输出**:

```text
=== RUN   TestConnectSway
--- PASS: TestConnectSway (0.00s)
PASS
ok  	github.com/xogas/wayland	1.008s
```

- **结论**: PASS; 在真实 sway 会话上 Connect 成功后 Close 返回 nil.
