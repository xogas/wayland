# ADR-006: 生成类型的跨包接缝与元数据扩展

- **状态**: Accepted
- **日期**: 2026-11-15
- **决策人**: 库作者
- **触发**: 生成 API 新增 (S-02 跨包 typed 对象与 G04 元数据)
- **不可逆性**: 低

## 背景

S-01 的生成类型持有未导出的 `p *Proxy`. S-02 要在 `protocols/` 的多个包之间传递 typed 对象, 并在 `new_id` 返回值里构造目标包的对象:

- `xdg_wm_base.get_xdg_surface(surface *wayland.Surface)` 需要从 `*Surface` 取 `*Proxy`;
- `zxdg_toplevel_decoration_v1.get_decoration(toplevel *xdgshell.Toplevel)` 需要跨包取 `*Proxy`;
- 包 A 的 `new_id` 返回包 B 的 typed 对象时, 需要构造 B 的未导出字段.

冻结契约只写了"生成类型内部持 `*wayland.Proxy`", 没有定义跨包读取与构造的接缝.

同时, AC-G04 要求元数据覆盖 `frozen`, `deprecatedSince`, `bitfield`, enum entry `since` 等字段, S-01 的 `Interface` / `Message` / `ArgSpec` 字段不够.

## 备选方案

| 方案 | 优点 | 缺点 |
| :--- | :--- | :--- |
| A. `Proxy()` + `New<T>` | 不导出字段; 跨包可用; 用户也可用 | 新增生成 API |
| B. 导出 `Proxy` 字段 + `New<T>` | 字段直接可读 | 公共面更大, 可被外部改写 |
| C. 跨包参数改为 `*wayland.Proxy` | 不新增构造器 | 失去跨包 typed API, 改冻结契约 |

## 决策

- 选择 A: 每个生成类型新增:
  - `func (t *T) Proxy() *wayland.Proxy`
  - `func NewT(p *wayland.Proxy, h THandler) *T` (有事件)
  - `func NewT(p *wayland.Proxy) *T` (无事件)
  - `Display` 无事件 handler, 生成 `NewDisplay(p *wayland.Proxy) *Display`.
- 生成代码在跨包对象参数上调用 `arg.Proxy().ID()`; 在跨包 `new_id` 上调用目标包的 `NewT`.
- 不改变任何已有签名; 只新增生成符号. 用户可以用这些符号, 但它们首先是生成代码的接缝.
- 元数据扩展:
  - `Interface.Frozen bool`, `Interface.Enums []Enum`.
  - `Message.DeprecatedSince uint32`, `Message.Destructor bool`.
  - `ArgSpec.Since uint32`, `ArgSpec.DeprecatedSince uint32`.
  - 新增 `Enum` 与 `EnumEntry`.
- 全部 interface 元数据仍在根包生成; `InterfaceByName` 按 stable 优先; 重名 interface 的失败方在自己的协议包内生成局部元数据, 供本包 `new_id` 使用; 根包保留 `allInterfaces` 供 G04 检查.
- 重名 interface 的失败方不生成 `Bind<T>` helper; stable 优先胜出方生成. 失败方仍生成类型与 `New<T>`.
- `Message.Destructor` 由生成器写入; 运行时 `sendRequest` 在成功发送 destructor 后把对象转入 destroyPending.

## 后果

- 正面: 跨包 typed 对象可编译; 不改已有签名; G04 元数据完整; 生成代码不再依赖未导出字段.
- 负面: 生成 API 增加 `Proxy()` 与 `New<T>`; 重名失败方没有 bind helper; 需要在契约增补中记录.
- 需要同步修改: `docs/contracts.md` 增补; 生成器; 根包元数据; S-02 测试与证据.
- 以后改回: 删除生成符号并恢复跨包 `*wayland.Proxy`; 需要新 ADR.

## 不可逆性

- 分级: 低. 新增符号可保留; 回退只影响生成代码.
