# 契约: wayland

- **状态**: Frozen
- **冻结日期**: 2026-11-15
- **对应**: `docs/spec.md` v1.0, ADR-001 到 ADR-005, `docs/design/modules.md`
- **范围**: 公共运行时 API, 生成代码接缝, 生成 API 模板, 生成器命令. `Proxy` / `Arg` / `Interface` 是生成代码接缝; 普通调用方使用 `Display` 与协议类型.

## 1. 入口

| API | 输入 | 输出 | 错误 | 并发 / 顺序 | 幂等 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `Connect()` | 无 | `*Display` | `ErrSource` (E-01) / `ErrConnect` (E-02) | 不并发; 每次调用新连接 | 否 |
| `DialContext(ctx, path)` | `ctx`, socket 路径 | `*Display` | `ErrSource` / `ErrConnect`, `ctx.Err()` | 不并发 | 否 |
| `DialFD(fd)` | 已连接 fd | `*Display` | `ErrSource` / `ErrConnect` | 不并发; 接管 fd | 否 |

### 1.1 Connect 的来源解析

- 若 `WAYLAND_SOCKET` 存在, 解析为 fd; 非法返回 E-01; 库接管该 fd, `Close` 关闭它.
- 否则若 `WAYLAND_DISPLAY` 存在: 以 `/` 开头时作为绝对路径; 否则拼接 `XDG_RUNTIME_DIR` + `/` + `WAYLAND_DISPLAY`; `XDG_RUNTIME_DIR` 缺失返回 E-01.
- 否则尝试 `XDG_RUNTIME_DIR/wayland-0`.
- `Connect` 是唯一读环境变量的入口; `DialContext` / `DialFD` 不读环境. 这是 ADR-002 的显式例外.
- `Connect` 没有 context; `DialContext` 的 context 只约束建立连接; 建连后 context 不保存.

### 1.2 Display

`Display` 是 `wl_display` 的生成类型; 同时提供手写运行时方法.

| 方法 | 输入 | 输出 | 错误 | 并发 / 顺序 | 幂等 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `Sync(handler CallbackHandler)` | 非 nil handler | `*Callback` | `ErrClosed`, `ErrInvalidArgument`, 版本 / 发送错误 | 与其他请求并发安全 | 否 |
| `GetRegistry(handler RegistryHandler)` | 非 nil handler | `*Registry` | 同上 | 与其他请求并发安全 | 否 |
| `Dispatch()` | 无 | `error` | `ErrClosed`, `ErrIO`, `ErrMalformedMessage`, `ErrProtocolViolation`, `*ProtocolError`, `ErrNoHandler` | 与 Roundtrip 串行; 同一时刻一个 | 否 |
| `Roundtrip(ctx)` | `ctx` | `error` | 同 Dispatch, `ctx.Err()` | 与 Dispatch 串行 | 否 |
| `Close()` | 无 | `error` | 第一次关闭错误; 后续 nil; 回调内调用 `ErrInvalidOperation` | 并发安全; 等待分发退出 | 是 |

- `Dispatch` 每次只处理一条 wire message; 该消息的 handler 同步执行; 返回 nil 表示这条消息已处理. 连接为空时阻塞.
- `Roundtrip` 发送 `wl_display.sync`, 循环 Dispatch 直到 callback done; 先前事件按服务端顺序交付; 取消返回 `ctx.Err()`, 连接保持可用, 被取消的 callback 不再交付.
- `Close` 关闭底层 fd; 对 `DialFD` 接管 fd; 重复 Close 返回 nil.
- 回调里调用 `Close` 属于非法重入, 返回 `ErrInvalidOperation`, 不等待.

## 2. 生成代码接缝

### 2.1 Proxy

每个生成对象内部持有一个 `*wayland.Proxy`; 事件里的 object / new_id 参数以 `*Proxy` 暴露.

| 方法 | 输入 | 输出 | 错误 | 并发 / 顺序 |
| :--- | :--- | :--- | :--- | :--- |
| `ID()` | 无 | `uint32` | 无 | 只读 |
| `Version()` | 无 | `uint32` | 无 | 只读 |
| `Interface()` | 无 | `*Interface` | 无 | 只读 |
| `SetSink(sink EventSink)` | 非 nil sink | `error` | `ErrInvalidOperation` (已设置) | 事件开始前设置 |
| `Send(opcode, args...)` | opcode, 参数 | `error` | 版本 / since / 参数 / IO / 关闭错误 | 并发安全; 消息原子 |
| `NewObject(iface)` | 目标 interface | `*Proxy` | `ErrIDExhausted`, `ErrInvalidArgument` | 并发安全; 版本 = min(parent.version, iface.version) |
| `Release()` | 无 | `error` | `ErrInvalidOperation` (已发送) | 仅供生成代码在发送失败后释放未发送对象 |

`NewObject` 注册一个没有 sink 的对象; 生成代码创建 typed wrapper 后 `SetSink`. 事件到达没有 sink 的对象时, `Dispatch` 返回 `ErrNoHandler`; 该事件被消费, 连接保持可用.

### 2.2 Arg

`Arg` 是 `Send` 的参数载体, 由生成代码构造.

```go
func Int(v int32) Arg
func Uint(v uint32) Arg
func Fixed(v float64) Arg
func String(v *string) Arg
func Array(v []byte) Arg
func FD(v int) Arg
func NewID(id uint32) Arg
func Object(id uint32) Arg
```

- `Fixed` 把 24.8 值按最近 1/256 编码; 解码为精确 float64.
- `String(nil)` 编码 null; `String(&"")` 编码空 string.
- `Array(nil)` 编码 null 或空; 解码按空处理.
- `FD` 只传 fd, 库不关闭调用方的 fd.

### 2.3 Interface 元数据

```go
type Interface struct {
    Name     string
    Version  uint32
    Requests []Message
    Events   []Message
}
type Message struct {
    Name  string
    Since uint32
    Args  []ArgSpec
}
type ArgSpec struct {
    Name      string
    Type      ArgType
    Interface string
    Enum      string
    AllowNull bool
}
func InterfaceByName(name string) (*Interface, bool)
func MustInterface(name string) *Interface
```

- 元数据只读, 由生成器写入根包.
- `MustInterface` 只在生成代码的包级变量初始化中使用; 名称缺失属于生成器 bug, 允许 panic.
- 用户可用元数据实现通用事件处理.

### 2.4 EventSink 与 Global

```go
type EventSink interface {
    DispatchEvent(opcode uint16, args []Arg) error
}
type Global struct {
    Name      uint32
    Interface string
    Version   uint32
}
```

- 生成的 typed 对象实现 `EventSink`, 在内部把 `Arg` 转成 handler 调用.
- `Global` 是 registry 事件与 bind 的输入; 库不缓存 global.

## 3. 生成 API 模板

### 3.1 命名

- 每个协议包计算 interface 名的公共前缀 (`_` 边界) 并剥离; 版本后缀保留. 例: `wl_display` -> `Display`, `xdg_wm_base` -> `WmBase`, `zwp_tablet_tool_v2` -> `TabletToolV2`.
- 包内碰撞时, 该包全部 interface 回退为完整 CamelCase.
- Go 包名 = 协议目录名去掉 `-` / `_` 后小写; 例 `xdg-shell` -> `xdgshell`.
- 枚举类型 = `<Interface><Enum>`; 常量 = `<Interface><Enum><Entry>`.
- 请求方法 / 事件方法 = CamelCase; 例 `get_registry` -> `GetRegistry`, `global_remove` -> `GlobalRemove`.

### 3.2 类型与 handler

- 每个 interface 生成一个 struct; 内部持 `*wayland.Proxy` 与 typed handler.
- 有事件的 interface 生成 `<Type>Handler` 接口; 事件方法按 XML 顺序, 返回空.
- 创建对象时 handler 必须非 nil; nil 返回 `ErrInvalidArgument`. 无事件的 interface 不生成 handler 参数.
- 事件参数: int/uint/fixed/enum/string/array/fd 用生成类型; allow-null 的 string 用 `*string`; object / new_id 用 `*wayland.Proxy`.
- 请求参数: object 用对应生成类型指针; allow-null 允许 nil; string 用 `string` 或 `*string` (allow-null); array 用 `[]byte`; fd 用 `int`; fixed 用 `float64`; enum 用生成枚举类型.
- `new_id` 请求返回 typed `*T`; 若新 interface 有事件, 方法额外接收 `<T>Handler`.
- 请求方法先检查对象版本与 `since`; 不满足返回 `ErrSince` 或 `ErrVersion`, 不发送.
- destructor 请求方法名为 `Destroy` (或 XML 名); 发送后对象进入 destroyPending; 再发请求 `ErrObjectDestroyed`; ID 在 delete_id 后复用.

### 3.3 bind

- `Registry.Bind(g Global, version uint32) (*Proxy, error)` 是通用动态 bind; 校验 `1 <= version <= min(g.Version, interface.version)`; 未知 interface 返回 `ErrInvalidArgument`; 返回的 Proxy 没有 sink.
- 每个可 bind 的 interface 生成 typed helper, 例:
  - `wayland.BindCompositor(reg *Registry, g Global) (*Compositor, error)`
  - `wayland.BindShm(reg *Registry, g Global, h ShmHandler) (*Shm, error)`
  - `wayland.BindSeat(reg *Registry, g Global, h SeatHandler) (*Seat, error)`
  - `wayland.BindOutput(reg *Registry, g Global, h OutputHandler) (*Output, error)`
  - `xdgshell.BindWmBase(reg *wayland.Registry, g wayland.Global, h WmBaseHandler) (*WmBase, error)`
- typed helper 用 `min(g.Version, interface.version)` 作为请求版本; 运行时不缓存 global.
- `RegistryHandler`:
  - `Global(g Global)`
  - `GlobalRemove(name uint32)`

### 3.4 生成文件

- 根包: `protocol_gen.go` (core typed), `protocol_meta_gen.go` (全部 interface 元数据).
- 协议包: `<protocol>_gen.go`.
- 每个文件头: `// Code generated by waylandgen; DO NOT EDIT.`
- 输出不含时间戳与本机路径; 文件顺序按协议路径与 interface 名稳定.

## 4. 核心 exemplar

> 完整 68 个协议按同一模板生成; 下面只列示例与契约相关的接口.

### 4.1 Registry

- `RegistryHandler`: `Global(Global)`, `GlobalRemove(uint32)`.
- `Bind(g Global, version uint32) (*Proxy, error)`.
- typed helpers 见 3.3.
- `Registry` 不缓存 global; 重复 bind 创建新对象.

### 4.2 Compositor / Surface / Region

- `Compositor.CreateSurface(h SurfaceHandler) (*Surface, error)`
- `Compositor.CreateRegion() (*Region, error)`
- `Surface.Attach(buffer *Buffer, x, y int32) error`; `buffer` 为 nil 表示 detach.
- `Surface.Damage(x, y, width, height int32) error`
- `Surface.Commit() error`
- `Surface.Destroy() error`
- `Surface.Frame(h CallbackHandler) (*Callback, error)`
- `Surface.SetBufferScale(scale int32) error`
- `Region.Destroy() error`

### 4.3 Shm

- `ShmHandler.Format(format uint32)`.
- `Shm.CreatePool(fd, size int32) (*ShmPool, error)`; fd 由调用方关闭, 库不关闭.
- `ShmPool.CreateBuffer(offset, width, height, stride int32, format uint32, h BufferHandler) (*Buffer, error)`
- `ShmPool.Destroy() error`; `Buffer.Destroy() error`
- `BufferHandler.Release()`.

### 4.4 Seat / Pointer / Keyboard

- `SeatHandler.Capabilities(caps uint32)`, `Name(name string)`.
- `Seat.GetPointer(h PointerHandler) (*Pointer, error)`
- `Seat.GetKeyboard(h KeyboardHandler) (*Keyboard, error)`
- `Seat.GetTouch(h TouchHandler) (*Touch, error)`
- `PointerHandler`: `Enter(serial uint32, surface *wayland.Proxy, sx, sy float64)`, `Leave(serial uint32, surface *wayland.Proxy)`, `Motion(time uint32, sx, sy float64)`, `Button(serial, time, button, state uint32)`, `Axis(time, axis uint32, value float64)`, `Frame()`, `AxisSource(source uint32)`, `AxisStop(time, axis uint32)`, `AxisDiscrete(axis uint32, value int32)`.
- `KeyboardHandler`: `Keymap(format uint32, fd int, size uint32)`, `Enter(serial uint32, surface *wayland.Proxy, keys []byte)`, `Leave(serial uint32, surface *wayland.Proxy)`, `Key(serial, time, key, state uint32)`, `Modifiers(serial, depressed, latched, locked, group uint32)`, `RepeatInfo(rate, delay int32)`.
- `TouchHandler`: `Down(serial, time uint32, surface *wayland.Proxy, id int32, x, y float64)`, `Up(serial, time uint32, id int32)`, `Motion(time uint32, id int32, x, y float64)`, `Frame()`, `Cancel()`, `Shape(id int32, x, y float64)`.
- 收到的 fd (keymap) 归调用方; 调用方负责关闭.

### 4.5 Output

- `OutputHandler.Geometry(x, y, physicalWidth, physicalHeight, subpixel int32, make, model string, transform int32)`, `Mode(flags, width, height, refresh int32)`, `Done()`, `Scale(factor int32)`, `Name(name string)`, `Description(description string)`.
- `Output.Release() error`.

### 4.6 Xdg shell

- `BindWmBase(reg *wayland.Registry, g wayland.Global, h WmBaseHandler) (*WmBase, error)`.
- `WmBaseHandler.Ping(serial uint32)`.
- `WmBase.GetXdgSurface(surface *Surface, h XdgSurfaceHandler) (*XdgSurface, error)`
- `WmBase.Pong(serial uint32) error`; `WmBase.Destroy() error`.
- `XdgSurfaceHandler.Configure(serial uint32)`.
- `XdgSurface.GetToplevel(h XdgToplevelHandler) (*XdgToplevel, error)`
- `XdgSurface.AckConfigure(serial uint32) error`; `XdgSurface.Destroy() error`.
- `XdgToplevelHandler.Configure(width, height int32, states []byte)`, `Close()`.
- `XdgToplevel.SetTitle(title string) error`; `SetAppID(appID string) error`; `Destroy() error`.

### 4.7 Callback

- `CallbackHandler.Done(callbackData uint32)`.
- `Display.Sync(h CallbackHandler) (*Callback, error)`.
- `wl_callback` 没有 destructor; 服务端在 done 后销毁.

## 5. 生成器命令

- 模块根 `//go:generate go run ./cmd/waylandgen`.
- 默认输入 `wayland-protocols/`; 默认输出模块根; `-root` / `-out` 只用于测试或显式运行.
- 成功退出 0; 失败非 0, 错误含文件与字段; 原子写入, 不留半成品.
- 同一输入两次生成, 第二次 `git diff --exit-code` 为 0.
- 生成器不是公共 API; `cmd/waylandgen` 是单个 main 包, 按 parse / model / render 文件拆分; 不建子包.

## 6. 错误

| E-ID | 哨兵 / 类型 | 触发 | 重试 |
| :--- | :--- | :--- | :--- |
| E-01 | `ErrSource` | 来源缺失 / 非法 / 路径过长 | 否, 修正环境 |
| E-02 | `ErrConnect` | 连接失败 | 调用方可重试 |
| E-03 | `ErrClosed` | 已关闭上 send / dispatch / roundtrip | 否 |
| E-04 | `ErrIO` | 读 / 写失败, EOF, reset | 调用方可重连 |
| E-05 | `*ProtocolError` | `wl_display.error` | 否 |
| E-06 | `ErrMalformedMessage` | 长度 / 对齐 / body 不匹配 | 否 |
| E-07 | `ErrProtocolViolation` | 未知对象 / opcode / delete_id | 否 |
| E-08 | `ErrVersion` | bind 版本超出 | 可降低版本 |
| E-09 | `ErrSince` | 请求 since 超出对象版本 | 否 |
| E-10 | `ErrInvalidArgument` | 非法 null / fd / interface / enum | 否 |
| E-11 | `ErrObjectDestroyed` | destroyPending / destroyed 上发送 | 否 |
| E-12 | `context.Canceled` / `context.DeadlineExceeded` | Roundtrip 取消 / 超时 | 可重试 |
| E-13 | `*GeneratorError` (cmd) | 输入 / 语义错误 | 否 |
| E-14 | `*GeneratorError` (cmd) | 写入错误 | 修正文件系统 |
| E-15 | `ErrInvalidArgument` | 动态 new_id 未知接口 | 否 |
| E-16 | `ErrIDExhausted` | 客户端 ID 耗尽 | 否 |
| - | `ErrNoHandler` | 事件对象没有 sink | 设置 sink 后继续 |
| - | `ErrInvalidOperation` | 回调内 Close / 重复 SetSink / Release 已发送对象 | 否 |

- `*ProtocolError` 的 `Is(ErrProtocolViolation)` 返回 true; 字段 `ObjectID`, `Interface`, `Code`, `Message`.
- 所有错误用 `fmt.Errorf("<操作>: %w", err)` 包装; 不用字符串比较.
- `context.Canceled` / `DeadlineExceeded` 原样返回.

## 7. 并发, 顺序, 幂等, FD, 性能

- `Dispatch` / `Roundtrip` 同一时刻一个; 其他调用等待; 回调在调用者 goroutine 上串行.
- 请求方法可在多个 goroutine 并发调用; 每条消息原子写入; 同一 goroutine 内顺序保持; 跨 goroutine 只保证原子性.
- 事件按服务端到达顺序交付; `Dispatch` 每次一条 message.
- `Close` 幂等; 等待分发退出; 关闭 fd; 重复 Close nil.
- destructor 请求发送后对象 destroyPending; 再发请求 `ErrObjectDestroyed`; delete_id 后 ID 复用.
- 发送的 fd 库不关闭; 收到的 fd 归调用方; 跳过未知事件的 fd 由库关闭.
- 无 v0.1.0 性能 SLA; 单连接内存随活动对象增长; 读缓冲上界 65535; 无后台 goroutine; 无无界队列.
- handler panic 不被库 recover; 它属于程序员错误, 调用方应 Close.

## 8. G3 审计收窄

> 本节关闭 G3 自由度审计中需要猜的硬约束; 未列的实现口味按 ADR 与 modules 放行.

### 8.1 目录与文件

- tier 由 XML 的父目录名决定 (stable / staging / unstable / experimental); 不内置 tier 表.
- 只在输入存在对应 XML 时创建 tier 与协议目录; 不建空目录.
- 生成文件名: 协议目录名把 `-` 换 `_`, 加 `_gen.go`; 包路径保留原目录名.
- 不同 tier 同目录名: 包名相同, import 路径区分; 不加 tier 前缀.
- 命名碰撞只按 interface 类型名判定; enum / 常量带 interface 前缀, 不参与回退.
- 根包文件拆分, `go:generate` 所在文件, 测试夹具放置: 内部实现, 放行; 不新增模块清单外的包.
- `protocols/` 下不放 `_test.go`; 测试集中在根包或 cmd 的 `_test.go`; R6 生成头不变.

### 8.2 生成 API

- destructor 方法名 = XML request 名 CamelCase; `release` -> `Release`, `destroy` -> `Destroy`.
- 协议包不导出自己的元数据; 统一 `wayland.InterfaceByName` / `MustInterface`.
- 每个 interface 生成 typed bind helper `Bind<Type>`; 有事件带 handler, 无事件不带; helper 用 `min(g.Version, interface.version)`.
- `EventSink.DispatchEvent` 返回 error 时, `Dispatch` 停止并返回 `ErrProtocolViolation` 包装; 连接关闭; 事件已消费.

### 8.3 线协议

- `Array(nil)` 编码长度 0; null 与空 array 不区分; 解码为空.
- `Fixed` 用 `math.Round` (远离零) 编码到 24.8.
- enum 底层类型: 默认 `int32`; bitfield 或任一值大于 MaxInt32 时用 `uint32`; 生成器校验.
- 多 fd 消息: 每条消息一次 `sendmsg`, 带全部 fd; 部分写重试同一 ancillary; 不按 fd 数量分批.

### 8.4 运行时

- E-01 `ErrSource`; E-02 `ErrConnect`; 两者不共用哨兵.
- E-04 / E-05 / E-06 / E-07 之后连接转 closed; 后续操作 `ErrClosed`.
- Roundtrip 取消: 在对象表保留 tombstone 到 done / delete_id; 期间事件丢弃; 不交付 callback; 连接可用.
- ID 分配: 从 1 到 0xFEFFFFFF; 单调递增 + delete_id 回收的空闲表; 0 保留; `Release` 只回收未发送对象.
- 错误消息的操作词是内部措辞; 必须含操作 / interface / message; 不固定词表.

### 8.5 生成器与示例

- 生成器校验: wayland.dtd, 必填属性, interface / enum 引用, opcode 连续, since / version, enum 值, allow-null, 重名, 跨协议 stable 优先; 失败不写输出.
- 示例目录: registry, shm-surface, xdg-shell, input, output; 成功退出 0, 失败退出 1; 不提供 flag / usage 子命令.

## 9. 指针

- 行为与 AC: `docs/spec.md` v1.0.
- 决策: `docs/adr/ADR-001` 到 `ADR-005`.
- 模块与依赖: `docs/design/modules.md`.
- 失败分类: `docs/design/failure.md`.
- 检查: `scripts/check-dep.sh`.
