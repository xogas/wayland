# G3 自由度审计: wayland (2026-11-15)

- **输入材料**: `docs/contracts.md`, `docs/design/modules.md`, `docs/design/failure.md`
- **独立会话**: A (single session; 未读 spec / ADR / 对话 / 代码)
- **审计输出**: 25 个需要实现者决定的点; 21 个已收窄到设计文件; 4 个建议放行.
- **结论**: 没有硬约束区 (目录结构 / 导出签名 / 错误集合 / 依赖边 / 状态迁移) 落在放行项里.
- **G3 状态**: 2026-11-15 用户确认 G3 通过; ADR Accepted; contracts / modules / failure Frozen.

## 输出 A: 目录树

```text
.
├── go.mod
├── generate.go                 # go:generate 指令
├── conn.go                     # Connect / DialContext / DialFD
├── display.go                  # Display 手写运行时方法
├── dispatch.go                 # 读一条消息, 解码, 路由
├── marshal.go                  # Arg 编解码, SCM_RIGHTS
├── object.go                   # 对象表, ID 分配与回收
├── errors.go                   # 哨兵, ProtocolError, 包装
├── protocol_gen.go             # wayland.xml 生成类型
├── protocol_meta_gen.go        # 68 个协议的 Interface 元数据
├── cmd/waylandgen/
│   ├── main.go
│   ├── parse.go
│   ├── model.go
│   ├── render.go
│   └── testdata/
├── protocols/<tier>/<proto>/<proto>_gen.go
├── examples/{registry,shm-surface,xdg-shell,input,output}/main.go
└── scripts/check-dep.sh
```

## 输出 A: 文件职责

- `conn.go`: 环境变量与 socket 路径解析, 建连, fd 接管.
- `display.go`: Sync / GetRegistry / Dispatch / Roundtrip / Close 与 closed 状态.
- `dispatch.go`: 单条 wire message 解码, opcode 定位, 事件交付.
- `marshal.go`: 小端编码, fixed / string / array / fd 参数.
- `object.go`: ID 到对象的映射, delete_id 回收, destroyPending 标记.
- `errors.go`: E-01 到 E-16 哨兵, `*ProtocolError`, 统一 `%w` 包装.
- `protocol_gen.go`: `wayland.xml` 生成类型.
- `protocol_meta_gen.go`: 全部 interface 元数据, 供动态 bind 与 `InterfaceByName`.
- `cmd/waylandgen/main.go`: 装配 parse -> model -> render, 原子写.
- `cmd/waylandgen/parse.go`: XML 到结构体.
- `cmd/waylandgen/model.go`: 语义校验, tier / 包名 / 类型名计算.
- `cmd/waylandgen/render.go`: 稳定顺序渲染, 生成头.
- `protocols/...`: 单协议 typed 类型, 请求方法, handler, 枚举.
- `examples/...`: 只演示, 不提供库行为.
- `scripts/check-dep.sh`: import 白名单, 包名与生成头检查.

## 审计点处置

| ID | 需要决定的点 | 处置 | 落点 |
| :--- | :--- | :--- | :--- |
| D01 | 根包文件拆分粒度 | 放行 | 不新增包; 只影响根包内部 |
| D02 | Display 双身份 (生成类型 + 手写方法) | 已定 | contracts 1.2 / 3.2 |
| D03 | go:generate 写在哪个文件 | 放行 | 模块根, 只一条指令 |
| D04 | 协议包内能否放测试 | 收窄 | modules R9; contracts 8.1 |
| D05 | 测试替身是否新建包 | 放行 | 只用 `_test.go`; 不新增清单外包 |
| D06 | XML 到 tier 目录的映射 | 收窄 | contracts 8.1; modules R8 |
| D07 | experimental 空目录 | 收窄 | contracts 8.1; 只在输入存在时创建 |
| D08 | 包名 / 前缀剥离 / 枚举命名 | 已定 | contracts 3.1 |
| D09 | 生成文件名规范化 | 收窄 | contracts 8.1; modules R8 |
| D10 | 不同 tier 同名目录 | 收窄 | contracts 8.1; 包名相同, import 路径区分 |
| D11 | 碰撞回退的判定范围 | 收窄 | contracts 8.1; 只看 interface 类型名 |
| D12 | handler 生成与 nil 规则 | 已定 | contracts 3.2 |
| D13 | destructor 命名 | 收窄 | contracts 8.2; XML 请求名 CamelCase |
| D14 | 协议包是否自带元数据 | 收窄 | contracts 8.2; modules R10; 根包唯一 |
| D15 | 哪些 interface 生成 bind helper | 收窄 | contracts 8.2; 全部 interface |
| D16 | EventSink 返回 error 的后果 | 收窄 | contracts 8.2; Dispatch 停止并关闭 |
| D17 | array 的 null 表示 | 收窄 | contracts 8.3; 长度 0 |
| D18 | fixed 的舍入 | 收窄 | contracts 8.3; math.Round 远离零 |
| D19 | enum 底层类型 | 收窄 | contracts 8.3; int32 或 uint32 规则 |
| D20 | 多 fd 消息的发送 | 收窄 | contracts 8.3; 每条消息一次 sendmsg |
| D21 | 小端 / 缓冲上界 / fd 所有权 | 已定 | contracts 7 |
| D22 | E-01 / E-02 的错误区分 | 收窄 | contracts 8.4 / 6; failure; `ErrSource` / `ErrConnect` |
| D23 | 协议 / IO 错误后的状态 | 收窄 | contracts 8.4; failure; closed + `ErrClosed` |
| D24 | 无 sink 事件 | 已定 | contracts 2.1; `ErrNoHandler` |
| D25 | Roundtrip 取消后的 callback | 收窄 | contracts 8.4; tombstone 到 done / delete_id |
| D26 | ID 分配与 Release | 收窄 | contracts 8.4; 单调递增 + 空闲表 |
| D27 | ID 上界与 0 保留 | 收窄 | contracts 8.4; 1 到 0xFEFFFFFF |
| D28 | Close 幂等与回调内 Close | 已定 | contracts 1.2 / 7 |
| D29 | 错误包装的操作词 | 放行 | 只要求含操作 / interface / message |
| D30 | Dispatch / Roundtrip 串行 | 已定 | contracts 7 |
| D31 | 生成器默认输入输出 / 原子 / 确定性 | 已定 | contracts 5 |
| D32 | 生成器语义校验清单 | 收窄 | contracts 8.5; DTD + 语义 + 跨协议 |
| D33 | 跨协议 import / stable 优先 | 已定 | contracts 8.2; modules R7 |
| D34 | 5 个示例 / main-only / 边界日志 | 已定 | contracts 8.5; modules |
| D35 | 示例目录名与退出码 | 收窄 | contracts 8.5; 成功 0, 失败 1 |

## 放行项 (建议)

- **D01 根包文件拆分**: 放行到"不新增包"为止; 文件如何切分由实现决定.
- **D03 go:generate 所在文件**: 放行到"模块根, 只一条指令"为止.
- **D05 测试替身放置**: 放行到"只用 `_test.go`, 不新增模块清单外的包"为止.
- **D29 错误操作词**: 放行到"必须含操作 / interface / message"为止; 不固定文案表.

## 收窄项回补

- 21 项收窄已写入 `docs/contracts.md` 第 8 节, `docs/design/modules.md` R8-R10, `docs/design/failure.md`.
- 没有把硬约束区 (目录结构 / 导出签名 / 错误集合 / 依赖边 / 状态迁移) 留在放行项.

## 用户处置

- 2026-11-15 用户决定生成器平铺为单个 main 包; ADR-004, modules.md, contracts.md, check-dep.sh 已同步; 未新增审计点.
- 2026-11-15 用户确认 4 个放行项与整体设计; G3 通过.
- ADR 标 Accepted; `docs/contracts.md` / `docs/design/modules.md` / `docs/design/failure.md` 标 Frozen.
