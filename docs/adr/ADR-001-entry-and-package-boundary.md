# ADR-001: 主入口与包边界

- **状态**: Accepted
- **日期**: 2026-11-15
- **决策人**: 库作者
- **触发**: 改变对外契约 (公共入口与导入路径)
- **不可逆性**: 高

## 背景

`docs/spec.md` 要求 `wayland.xml` 的绑定在模块根包, 其余协议在 `protocols/` 下; 运行时需要 Unix socket, 对象表, 事件分发; 生成包需要发送请求. `wl_display` 是唯一在连接建立时就存在的 Wayland 协议对象; `Conn` 是 Go 运行时概念, 不是协议接口. 主入口应该贴近协议, 同时给生成包留一个稳定的发送接缝.

## 备选方案

| 方案 | 优点 | 缺点 |
| :--- | :--- | :--- |
| A. `Connect() (*Conn, error)` | 运行时与生成类型分开; 生成代码简单 | 用户多一个非协议概念; 与 `wl_display` 入口不一致 |
| B. `Connect() (*Display, error)` | 入口即协议对象; 用户只面对 `Display` 与协议类型 | 生成的 `Display` 要携带运行时状态; 需要 `Proxy` 接缝 |
| C. `Connect() (*Client, error)` | 可扩展 | 多一层命名; 当前不需要 |

## 决策

选择 B. `Connect() (*Display, error)`.

- 根包 `wayland` 同时包含手写运行时, `wayland.xml` 的生成类型, 以及全部 interface 元数据.
- `Display` 有生成请求方法 (`Sync`, `GetRegistry`) 与手写运行时方法 (`Dispatch`, `Roundtrip`, `Close`).
- socket, 对象表, 分发状态放在不导出的 `conn` 中; 生成包通过导出的 `Proxy` 基类发送请求与创建对象.
- `protocols/` 下的生成包只 import 根包和生成器解析出的跨协议目标包; 根包不 import `protocols/`.
- 生成器在 `cmd/waylandgen`; 它的子包不进入公共 API.

## 后果

- 正面: 用户只需要理解 `Display` 与协议类型; 生成包依赖根包, 不存在根包 -> 协议包的反向依赖; `Proxy` 是唯一的生成代码接缝.
- 负面: 根包同时承担运行时, core 类型与全量元数据, 体积较大; `Proxy` / `Arg` / `Interface` 会成为公开接缝, 必须写进契约; 生成的 `Display` 类型与手写方法必须共享同一份未导出状态.
- 需要同步修改: `docs/contracts.md`, `docs/design/modules.md`, `AGENTS.md` 的指针与依赖规则.

## 不可逆性

- 以后改回: 改 `Connect` 签名, 所有示例与生成代码的入口, 以及 `contracts.md`; 发布后需要 major 版本.
- 分级: 高.
- 回滚方案: v0.1.0 前可整体重写; 发布后保留 `Conn` 兼容入口需要新 ADR 与一个 major 周期.
