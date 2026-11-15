# AC-O01: 对象 ID 分配

- **切片**: S-01.
- **测试**: TestObjectIDAllocation
- **命令**: `go test -race -v -run '^TestObjectIDAllocation$' .`
- **原始输出**:

```text
=== RUN   TestObjectIDAllocation
--- PASS: TestObjectIDAllocation (0.00s)
PASS
ok  	github.com/xogas/wayland	1.007s
```

- **结论**: PASS; 显示对象占 1, 新对象从 2 开始; release 后复用; 未知 delete_id 为 ErrProtocolViolation.
