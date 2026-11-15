# AC-O03: 未知对象与 delete_id

- **切片**: S-01.
- **测试**: TestDispatchUnknownObject, TestDispatchDeleteID
- **命令**: `go test -race -v -run '^(TestDispatchUnknownObject|TestDispatchDeleteID)$' .`
- **原始输出**:

```text
=== RUN   TestDispatchUnknownObject
--- PASS: TestDispatchUnknownObject (0.00s)
=== RUN   TestDispatchDeleteID
--- PASS: TestDispatchDeleteID (0.00s)
PASS
ok  	github.com/xogas/wayland	1.011s
```

- **结论**: PASS; 未知对象事件返回 ErrProtocolViolation 并关闭; delete_id 移除对象.
