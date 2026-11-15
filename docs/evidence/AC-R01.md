# AC-R01: Roundtrip

- **切片**: S-01.
- **测试**: TestRegistrySwayRoundtrip
- **命令**: `go test -race -v -run '^TestRegistrySwayRoundtrip$' .`
- **原始输出**:

```text
=== RUN   TestRegistrySwayRoundtrip
--- PASS: TestRegistrySwayRoundtrip (0.00s)
PASS
ok  	github.com/xogas/wayland	1.010s
```

- **结论**: PASS; 真实 sway 上 GetRegistry 后 Roundtrip 收到 global 事件并返回 nil.
