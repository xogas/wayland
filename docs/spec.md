# Spec: wayland / 客户端库与生成器

- **版本**: v1.0
- **状态**: Frozen
- **对应立项单**: `docs/charter.md` v1.0
- **对应意图包**: `docs/intent.md` v1.0
- **冻结日期**: 2026-11-15

## 1. 背景与目标

本规格覆盖 v0.1.0 的四个部分: 开发期生成器, 客户端运行时, 68 个协议的 Go 绑定, 5 个示例与 registry-bind 验证. 目标与边界引用 `docs/intent.md` 和 `docs/charter.md`, 不复述.

本规格锁外部可观察行为. 公共 API 名称, 包内类型布局, 内部数据结构与算法不属于本规格, 由 L3 契约定; 本规格用行为描述代替函数名.

## 2. 范围内的行为

| ID | 功能 | 行为描述 | 验收标准 |
| :--- | :--- | :--- | :--- |
| F-01 | 生成器 | 仓库根 `go generate ./...` 递归读取 `wayland-protocols/` 下 68 个 XML; 校验 DTD 与语义; 生成根包与 `protocols/` 下的绑定; 生成失败不写半成品; 同一输入两次生成无 diff | AC-G01 到 AC-G07 |
| F-02 | 连接与关闭 | 按标准顺序解析 socket: `WAYLAND_SOCKET`, 然后 `WAYLAND_DISPLAY` + `XDG_RUNTIME_DIR`, 最后 `wayland-0`; 建立连接; Close 幂等并释放资源 | AC-C01 到 AC-C03 |
| F-03 | 线协议编解码 | 读写 32 位头与参数; 主机小端; 32 位对齐; 支持 int, uint, fixed, string, array, fd, new_id, object; 畸形消息返回错误, 不 panic | AC-W01 到 AC-W07 |
| F-04 | 对象与 ID 生命周期 | 客户端 ID 分配与复用; 销毁请求; `wl_display.delete_id`; 对象 0 为 null; 未知对象或 opcode 返回协议错误 | AC-O01 到 AC-O04 |
| F-05 | 事件分发与并发 | 事件按对象回调串行交付; 回调里可以发请求; 不止一个 goroutine 可以同时发请求; 消息原子写入; Close 可唤醒阻塞的分发 | AC-D01 到 AC-D05 |
| F-06 | 版本协商与 since | 绑定版本校验; `since` 大于对象版本时拒绝请求; 动态 `new_id` 解析; registry global 事件按顺序交付 | AC-V01 到 AC-V05 |
| F-07 | 阻塞, 往返与 context | Dispatch 阻塞到事件或错误; Roundtrip 带 context; 发送在 socket 满时阻塞, Close 可唤醒 | AC-R01 到 AC-R03 |
| F-08 | 错误模型 | 集中类型化错误: 连接, IO, 协议, 消息格式, 对象, 版本, 参数, 已关闭, 生成器; 调用方可用 `errors.Is` / `errors.As` 区分 | AC-E01 到 AC-E03 |
| F-09 | 5 个示例 | registry, shm-surface, xdg-shell, input, output; 在 sway 上独立运行并退出 0; 无显示时给出错误并非 0 退出 | AC-X01 到 AC-X07 |
| F-10 | registry-bind | 对 sway 广告的每个 global 按广告版本绑定; 有生成绑定的用 typed bind; 未知接口用测试内 raw bind; 跳过未知事件; 跑一轮 roundtrip 确认无 protocol error | AC-RB01 到 AC-RB03 |
| F-11 | 验证链 | DTD 校验, 生成元数据一致性, 语义不变量, 全签名编解码覆盖 | AC-G04, AC-G05, AC-G07, AC-W02, AC-RB01 |

## 3. 数据规格

> 字段名与类型是语义契约; Go 类型名与导出方式由 L3 定. 所有字段来源为协议 XML 或运行时状态; 生成结果只由 XML 决定, 不含时间戳或本机路径.

| 实体 | 字段 | 类型 | 约束 | 来源 | 变更策略 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| Protocol | name | string | 非空; `[a-z0-9_]+` | XML | 上游更新后重新生成 |
| Protocol | interfaces | Interface 列表 | 至少 1 个; 同一协议内 interface 名唯一 | XML | 上游更新后重新生成 |
| Interface | name | string | 非空; `[a-z0-9_]+`; 同一协议内唯一 | XML | 上游更新后重新生成 |
| Interface | version | uint32 | 1 到 0xFFFFFFFF; 请求/事件/枚举的 since 不超过它 | XML | 上游更新后重新生成 |
| Interface | frozen | bool | 可选; XML `frozen` 属性; 不改变运行时行为 | XML | 上游更新后重新生成 |
| Request | name | string | 非空; 同一 interface 内唯一 | XML | 上游更新后重新生成 |
| Request | opcode | uint16 | 按 XML 顺序从 0 连续分配 | XML | 上游更新后重新生成 |
| Request | since | uint32 | 1 到 interface.version; 缺省 1 | XML | 上游更新后重新生成 |
| Request | deprecatedSince | uint32 | 可选; 不小于 since | XML | 上游更新后重新生成 |
| Request | type | string | 空或 `destructor`; 其他值拒绝 | XML | 上游更新后重新生成 |
| Event | name | string | 非空; 同一 interface 内唯一 | XML | 上游更新后重新生成 |
| Event | opcode | uint16 | 按 XML 顺序从 0 连续分配 | XML | 上游更新后重新生成 |
| Event | since | uint32 | 1 到 interface.version; 缺省 1 | XML | 上游更新后重新生成 |
| Event | deprecatedSince | uint32 | 可选; 不小于 since | XML | 上游更新后重新生成 |
| Arg | name | string | 非空; 同一消息内唯一 | XML | 上游更新后重新生成 |
| Arg | type | string | int / uint / fixed / string / array / fd / new_id / object 之一 | XML | 上游更新后重新生成 |
| Arg | interface | string | 仅 object 与 new_id 可填; 必须解析到 68 个 XML 中的 interface; 重名时 stable 优先, 仍有歧义返回 E-13; new_id 可缺省表示动态接口 | XML | 上游更新后重新生成 |
| Arg | enum | string | 仅 int 与 uint 可填; 必须解析到同一 interface 的 enum | XML | 上游更新后重新生成 |
| Arg | allowNull | bool | 仅 object / new_id / string / array 可为 true; 其他类型拒绝 | XML | 上游更新后重新生成 |
| Enum | name | string | 非空; 同一 interface 内唯一 | XML | 上游更新后重新生成 |
| Enum | since | uint32 | 1 到 interface.version; 缺省 1 | XML | 上游更新后重新生成 |
| Enum | bitfield | bool | 可选; 不改变线格式 | XML | 上游更新后重新生成 |
| EnumEntry | name | string | 非空; 同一 enum 内唯一 | XML | 上游更新后重新生成 |
| EnumEntry | value | int32 或 uint32 | 同一 enum 内唯一; 能由 32 位整数表示 | XML | 上游更新后重新生成 |
| EnumEntry | since | uint32 | 1 到 interface.version; 缺省 1 | XML | 上游更新后重新生成 |
| EnumEntry | deprecatedSince | uint32 | 可选; 不小于 since | XML | 上游更新后重新生成 |
| Message | objectID | uint32 | 0 表示 null; 客户端对象在 [1, 0xFEFFFFFF], 服务端对象在 [0xFF000000, 0xFFFFFFFF] | 线协议 | 协议文档更新 |
| Message | size | uint16 | 8 到 65535, 含 8 字节头 | 线协议 | 协议文档更新 |
| Message | opcode | uint16 | 0 到 interface 的 request/event 数减 1 | 线协议 | 协议文档更新 |
| Message | args | Arg 值序列 | 顺序与签名一致; 32 位对齐 | 线协议 | 协议文档更新 |
| Object | id | uint32 | 客户端或服务端范围; 0 不是对象 | 运行时 | L3 契约 |
| Object | interface | string | 必须是已解析的 interface | 运行时 | L3 契约 |
| Object | version | uint32 | 1 到 interface.version; 等于 bind 请求的 version | 运行时 | L3 契约 |
| Object | state | enum | alive / destroyPending / destroyed | 运行时 | L3 契约 |
| Global | name | uint32 | 服务端分配; 唯一 | registry 事件 | L3 契约 |
| Global | interface | string | 非空; 可以是 68 个 XML 之外的接口 | registry 事件 | L3 契约 |
| Global | version | uint32 | 广告版本; bind 输入; 不小于 1 | registry 事件 | L3 契约 |
| Error | kind | enum | 见第 5 节 E-ID | 运行时 / 生成器 | L3 契约 |
| Error | objectID | uint32 | 协议错误事件携带; 其他错误可为 0 | 运行时 | L3 契约 |
| Error | interface | string | 协议错误事件携带; 其他错误可为空 | 运行时 | L3 契约 |
| Error | code | uint32 | 协议错误事件携带; 其他错误可为 0 | 运行时 | L3 契约 |
| Error | message | string | 非空; 包含操作上下文 | 运行时 / 生成器 | L3 契约 |

**线格式补充**: int 为 32 位有符号; uint 为 32 位无符号; fixed 为 24.8 有符号并保持 32 位值; object 与 new_id 为 32 位对象 ID; string 长度包含结尾 NUL, 空 string 长度为 1 (仅 NUL), null string 长度为 0; array 长度 0 表示 null 或空, 非空为原始字节; fd 不占消息长度, 通过 Unix socket 的 ancillary data 传递, 每个 fd 参数对应 1 个 fd, 顺序与参数一致; 所有 32 位值按主机字节序, 本平台为小端.

**空值与 null**: object / new_id 的 0 表示 null, 仅当 allow-null 为 true 或该位置允许 null 时接受; string 的 0 长度为 null, 1 为空; array 的 0 长度按空处理; 非 allow-null 的 null 返回 E-10. 解码器必须区分空值与非法值, 不得 panic.

**bind 与版本**: bind 的输入是调用方持有的 Global (name / interface / version) 与请求 version; 库不缓存 global; 请求 version 必须在 1 到 min(Global.version, interface.version) 之间; 动态 new_id 创建的对象 version 等于 bind 请求的 version, 不自动取广告版本.

**delete_id**: 只对 destroyPending 或 destroyed 对象合法; alive 对象收到 delete_id, 重复 delete_id, 或未知 ID 的 delete_id 均为 E-07; 客户端 ID 只在收到 delete_id 后复用.

## 4. 状态与流转

### 4.1 连接

| 当前状态 | 允许操作 | 事件 | 下一状态 |
| :--- | :--- | :--- | :--- |
| disconnected | connect | 解析并连接成功 | connected |
| disconnected | connect | 解析或连接失败 | closed |
| connected | send / dispatch / roundtrip / close | 请求成功或阻塞 | connected |
| connected | close | 关闭开始 | closing |
| connected | 任意 | IO 错误或协议错误 | closed |
| closing | close | 等待资源释放 | closed |
| closed | close | 幂等 | closed |
| closed | send / dispatch / roundtrip | 拒绝 | closed |

非法迁移: 在 disconnected / closing / closed 上发送或分发返回 E-03; 连接不能从 closed 回到 connected; Close 可以从 connected / closing / closed 调用.

**连接来源**: 若 `WAYLAND_SOCKET` 存在, 视为已连接的 fd, 库接管并在 Close 关闭; 若 `WAYLAND_DISPLAY` 是绝对路径, 直接使用, 否则与 `XDG_RUNTIME_DIR` 拼接; 两者都没有时尝试 `wayland-0`; 拼接后路径超过 sun_path 上限返回 E-01.

**Close**: 第一次 Close 返回关闭错误 (若有), 后续 Close 返回 nil; 并发 Close 被串行化; Close 等待正在进行的 Dispatch / Roundtrip 退出; 已连接的 fd 由库关闭.

**Dispatch / Roundtrip 并发**: 同一时刻只允许一个 Dispatch 或 Roundtrip 运行, 其他调用等待; 回调在该调用者的 goroutine 上同步执行, 库不创建回调 goroutine; 因此回调不会并发, 调用方不需要额外加锁.

### 4.2 对象

| 当前状态 | 允许操作 | 事件 | 下一状态 |
| :--- | :--- | :--- | :--- |
| alive | 任意请求 / 事件 | 收到 destructor 请求发送 | destroyPending |
| alive | 任意请求 / 事件 | 收到服务端 delete_id | destroyed |
| destroyPending | 无新请求 | 收到 delete_id | destroyed |
| destroyPending | 无新请求 | 收到事件 | destroyPending, 事件丢弃 |
| destroyed | 无新请求 | ID 可复用 | destroyed |

非法迁移: destroyPending 或 destroyed 上发送请求返回 E-11; destroyed 之后收到该 ID 的事件返回 E-07 并关闭连接; 未知对象 ID 的事件返回 E-07.

**delete_id 与复用**: delete_id 只对 destroyPending / destroyed 对象合法; alive 对象, 重复 delete_id, 未知 ID 的 delete_id 都返回 E-07 并关闭连接; 客户端只在收到 delete_id 后复用 ID. destroyPending 上收到的该对象事件按状态机显式丢弃, 这是有意行为, 不是静默丢弃.

**重复 bind**: 同一个 global 可以 bind 多次, 每次创建独立对象, 由调用方持有; 库不自动销毁旧对象, 也不做 global 缓存.

### 4.3 往返

| 当前状态 | 事件 | 下一状态 |
| :--- | :--- | :--- |
| pending | 收到 wl_callback.done | done |
| pending | context 取消或超时 | canceled |
| pending | IO 或协议错误 | failed |

取消后连接保持可用. 被取消的 Roundtrip 不再交付回调; 该 callback 对象的事件按 delete_id 规则丢弃; 不消费其他对象的事件; 后续 Dispatch / Roundtrip 继续正常; 连接不因取消而关闭.

### 4.4 registry

registry 事件按服务端到达顺序交付, 不按名称排序; global 与 global_remove 谁先到谁先交付. 库不强制缓存; 调用方维护 global 表. bind 使用调用方提供的 Global (含广告 version) 与请求 version; 请求 version 必须在 1 到 min(Global.version, interface.version) 之间. 重复 bind 每次创建独立对象, 不自动销毁旧对象. global_remove 后的 bind 由调用方版本表决定; 服务端返回 error 时映射为 E-05, 版本不匹配映射为 E-08.

## 5. 错误与失败

> 错误标识在 L3 契约中映射为 Go 类型; 本规格锁语义. 调用方不得依赖错误字符串匹配.

| ID | 场景 | 触发条件 | 期望行为 | 可重试 |
| :--- | :--- | :--- | :--- | :--- |
| E-01 | 连接来源缺失或无效 | 环境变量缺失, `WAYLAND_SOCKET` 非法, 路径过长 | 返回连接来源错误, 含尝试过的来源; 不 panic | 否, 修正环境 |
| E-02 | 连接失败 | 权限, 拒绝, socket 不存在 | 返回 IO 或连接错误; 不 panic | 调用方可以重试 |
| E-03 | 已关闭 | 在 closed 上 send / dispatch / roundtrip | 返回已关闭错误 | 否 |
| E-04 | IO 错误 | 读或写失败, EOF, ECONNRESET; 消息中间 EOF | 返回 IO 错误; 连接转 closed | 调用方可以重新连接 |
| E-05 | 协议错误 | 收到 `wl_display.error` | 返回协议错误, 含 objectID / interface / code / message; 连接转 closed | 否 |
| E-06 | 消息格式错误 | 长度小于 8, 对齐错误, 完整帧声明与实际 body 不匹配, 或编码消息大于 65535 | 返回消息格式错误; 连接转 closed; 不 panic | 否 |
| E-07 | 未知对象 / opcode / delete_id | 事件对象不在表中, opcode 超出范围, 或 delete_id 非法 | 返回协议错误; 连接转 closed; 不向服务端发送 `wl_display.error` | 否 |
| E-08 | 版本不匹配 | bind 版本大于广告版本或 interface 版本, 或小于 1 | 返回版本错误; 不发送 bind; 连接保持 | 调用方可以降低版本 |
| E-09 | since 违例 | 请求的 since 大于对象版本 | 返回版本错误; 不发送 | 否 |
| E-10 | 参数无效 | 非 allow-null 的 0 对象, 负 fd, 未解析的 interface 或 enum | 返回参数错误; 不发送 | 否 |
| E-11 | 对象已销毁 | 在 destroyPending / destroyed 上发送 | 返回对象已销毁错误; 不发送 | 否 |
| E-12 | 上下文取消 | Roundtrip 的 context 取消或超时 | 返回 `context.Canceled` / `context.DeadlineExceeded`; 连接保持 | 调用方可以重试 |
| E-13 | 生成器输入或语义错误 | XML/DTD 无效, 空输入, 引用未解析, 跨协议重名无法 stable 优先解析, opcode 不连续, since 越界, enum 重复, 非法 allow-null | 生成器非 0 退出; 错误含文件与位置; 不写输出 | 否, 修正输入 |
| E-14 | 生成器写入错误 | 输出目录不可写, 权限, 磁盘错误, 非法 flag | 生成器非 0 退出; 原子写入, 不留半成品 | 修正文件系统后可重试 |
| E-15 | 动态 new_id 无法解析 | 动态 new_id 的接口名未知或版本不匹配 | 返回参数错误; 不发送 | 否 |
| E-16 | 客户端 ID 耗尽 | 客户端对象 ID 范围用完 | 返回资源错误; 不发送 | 否, 释放对象 |

**错误上下文**: 每个错误的消息包含操作名, interface (适用时), message (适用时); 生成器错误含文件与字段. 调用方可以用 `errors.Is` / `errors.As` 区分类别, 不依赖字符串.

**本地关闭**: E-06 / E-07 只关闭本地连接, 不向服务端发送 `wl_display.error`; 这是有意选择, 避免在已损坏的流上继续写.

**EOF 与截断**: 连接在消息中间 EOF 返回 E-04; 内存中的完整帧声明长度小于 8 / 对齐错误 / 声明的 body 与已读数据不匹配返回 E-06.

**panic 边界**: 外部输入, 线协议解码, 生成器输入不得触发 panic; 只能返回错误. 只有程序员错误 (例如 L3 契约明确要求非 nil 的参数为 nil) 可以 panic, 且必须在 L3 契约中列出.

**生成器失败规则**: 任何 E-13 / E-14 都不得留下部分生成文件; 先写临时目录, 全部成功后替换, 失败清理临时目录.

## 6. 边界与限制

### 6.1 限制

| 项 | 下限 | 上限 | 超限行为 |
| :--- | :--- | :--- | :--- |
| 消息长度 | 8 | 65535 | 编码大于 65535 返回 E-06; 解码 size 小于 8 或对齐错误返回 E-06; EOF 返回 E-04 |
| 客户端对象 ID | 1 | 0xFEFFFFFF | 分配返回 E-16 |
| 服务端对象 ID | 0xFF000000 | 0xFFFFFFFF | 不作为客户端分配 |
| interface 版本 | 1 | XML 声明值 | bind 超出返回 E-08 |
| since | 1 | interface.version | 超出返回 E-09 或 E-13 |
| string / array 长度 | 0 | 由消息长度限制 | string: null 0, 空 1, 非空含 NUL; array: 0 为 null / 空; 超出消息 E-06 |
| fd | 有效 fd | OS 限制 | 无效 fd 返回 E-10; fd 消息用 sendmsg 原子写, 部分写重试同一 ancillary |
| 并发发送 | 1 | 不设固定上限 | 库内部串行写入; 8 goroutine 是测试下限 |
| 并发分发 | 1 | 1 | 同一时刻只允许一个 Dispatch / Roundtrip; 其他调用等待 |
| Unix socket 路径 | 1 | OS 的 sun_path 上限 | 返回 E-01 |
| 生成输入 | 0 | 由根目录决定 | 空集 E-13; v0.1.0 的 68 个文件是验收基线; 生成器不硬编码 68 |
| 生成输出 | 68 个绑定 | 根包 + `protocols/` | 生成器创建目录; 不可写返回 E-14; 原子替换 |

**未知 global 接口**: sway 会广告 68 个 XML 之外的 wlroots / KDE 接口. 公共 API 不提供这些接口的 typed 绑定; registry-bind 测试可以用测试内 raw bind 发送 bind 请求, 按消息长度跳过未知事件. 这不是公开运行时能力, 也不扩大 68 个协议的范围.

### 6.2 异常与边界矩阵

| 输入维度 | 空 / 零值 | 极值 | 格式 / 类型错误 | 并发 | 超时 / 取消 | 重复 | 部分失败 |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 连接来源 / env | 缺变量: E-01 (AC-C02) | 路径过长: E-01 (AC-C02) | 非法 fd: E-01 (AC-C02) | N/A: 连接前无请求 | N/A: 本地 Unix connect 无 context; 失败立即可见 | N/A: 每次连接使用新对象 | 连接建立后首个 IO 失败: E-04 (AC-E02) |
| 消息头 / 长度 | 长度 0: E-06 (AC-W03) | 长度 65535 合法; 编码大于 65535: E-06 (AC-W07) | 未知 opcode: E-07 (AC-O03) | N/A: 单连接单读者 | N/A: 读阻塞由 Dispatch / Close 处理 | N/A | 内存截断: E-06 (AC-W03); 连接 EOF: E-04 (AC-E02) |
| 对象 ID / 状态 | 0 为 null; 非 allow-null 时 E-10 (AC-W05) | 范围端点: 合法; 耗尽: E-16 (AC-O04) | 未知对象 / 错误 interface: E-07 (AC-O03) | 并发发送按消息串行 (AC-D02) | N/A | alive / 重复 / 未知 delete_id: E-07 (AC-O03) | destroyPending 上发送: E-11 (AC-O02) |
| 版本 / since | version 0: E-08 (AC-V01) | version 大于广告或 interface: E-08 (AC-V01) | 非法 since 值: E-13 (生成器) (AC-G03) | 并发 bind 串行写入 (AC-D02) | N/A | 重复 bind: 允许, 每次创建新对象 (AC-V03) | N/A: 库不缓存 global; 调用方版本表决定; 失败 E-08 (AC-V01) 或 E-05 (AC-E01) |
| 参数 (string / array / fd / new_id) | null string 0 / 空 string 1 / array 0; 非法 null: E-10 (AC-W05) | 长度超出消息上限: E-06 (AC-W07) | 无效 fd: E-10 (AC-W06); 未知动态接口: E-15 (AC-V05) | 并发发送 fds 串行 (AC-D02) | N/A | 同一 fd 发送两次: 调用方负责, 库不关闭 (AC-W04) | fd 消息用 sendmsg 原子重试; 关闭返回 E-04 (AC-R03) |
| 并发发送 / 关闭 | 无发送者: Dispatch 阻塞 (AC-R01) | 8 个发送者: -race 通过 (AC-D02) | N/A | Dispatch / Roundtrip 并发: 串行 (AC-D05); Close 与发送并发: 不 panic; 一个先成功 (AC-C03) | Roundtrip ctx 取消: E-12 (AC-R02) | 连续 Close: 幂等 (AC-C01); Close 后 Dispatch: E-03 (AC-D03) | Close 中断发送: 返回 E-03 / E-04 (AC-R03) |
| 生成器输入 | 空 XML / 空目录: E-13 (AC-G02) | 输入集合全部处理; v0.1.0 的 68 个文件是基线; 单个超大文件按内存处理 | 非法 XML / DTD: E-13 (AC-G02) | N/A: 生成器按单进程运行; 并发运行由调用方避免 | N/A | 重复 interface / enum: E-13 (AC-G03) | 任一 XML 失败: 不写输出 (AC-G02) |
| 生成器输出 | 输出目录缺失: 生成器创建; 不可写 E-14 (AC-G06) | 磁盘满 / 权限: E-14 (AC-G06) | 非法 flag: E-14 (AC-G06) | N/A: 单进程 | N/A | 重复生成: 无 diff (AC-G01) | 写入中断: 原子替换, 无半成品 (AC-G06) |
| 示例 / 真实会话 | 无 WAYLAND_DISPLAY: 非 0 退出 (AC-X06) | 无 global: registry 退出非 0 (AC-X07); 多显示器: 逐个输出 (AC-X05) | 协议错误: E-05 (AC-E01) | N/A: 每个示例独立进程 | shm-surface 1 秒退出; xdg-shell / input 等 Esc (AC-X02 / AC-X03 / AC-X04) | 重复运行: 同一行为; 重复 global 按事件交付 | N/A: 每个示例独立进程; registry-bind 失败单独报错 (AC-RB03) |

> 矩阵没有空格. N/A 的原因已写在格内: 该维度在该场景没有外部可观察的独立行为.

### 6.3 有意放行与限制

- 公共 API 名称, 包内类型布局, 内部数据结构, 算法: L3 契约, 本规格不锁.
- 跨协议引用: 唯一 interface 按名称全局解析; 重名时 stable 优先; 仍有歧义由生成器返回 E-13; import 布局由 L3 定.
- 生成输出: 不含时间戳与本机绝对路径; 输出顺序按协议路径与接口名稳定.
- 测试资产: fake server, socketpair, 测试内 raw bind 属测试代码, 不进入公共 API; 位置由 L3 / L4 定.
- fd 所有权: 交付消息的接收 fd 归调用方; 跳过或未知事件中的接收 fd 由库关闭; 发送 fd 库不关闭.
- 回调 goroutine: 回调在 Dispatch / Roundtrip 调用者 goroutine 上同步执行; 库不创建回调 goroutine.
- input 示例: 打印 wl_keyboard 的原始 evdev keycode, Esc 为 1; pointer 坐标按 wl_fixed 十进制输出.
- 内部日志格式: 不锁; 错误返回必须含操作 / interface / 消息上下文.
- 性能 / 吞吐 / 内存: 不设 v0.1.0 阈值; L4 记录基线.
- 未知 global 接口: 测试内 raw bind; 公共 API 只覆盖 68 个 XML.
- sway 未广告协议: 只做生成, 元数据与编解码验证; 不声称真实连接可用.
- `wl_shell` 等废弃接口: 生成绑定; 示例不使用.
- stable 与 unstable 同名协议: 按源目录各自生成; 不合并.
- 上游协议升级: 人工替换 XML 后重新生成; 不做自动同步.

## 7. 非功能

引用 `docs/charter.md` 的非功能预算; 本规格相关行如下.

| 维度 | 目标 | 验证 |
| :--- | :--- | :--- |
| 构建与依赖 | linux/amd64, `CGO_ENABLED=0 go build ./...` 通过; 第三方 Go 依赖 0; `gofmt -l .` 为空; `go vet ./...` 通过; `go mod tidy` 无 diff | 命令 + 原始输出 |
| 确定性生成 | 两次生成, 第二次 `git diff --exit-code` 为 0 | 命令 + 原始输出 |
| 数据一致性 | 68 XML 通过 DTD; 解析与生成元数据一致; 语义不变量通过; 每条 request/event 至少 1 条编解码用例 | `go test` + DTD 命令 |
| 并发安全 | `go test -race ./...` 通过; 8 goroutine 发送无 race 与 deadlock; Close 后 goroutine 回到基线 | `go test -race` + 原始输出 |
| 互操作 | registry-bind 覆盖 sway 广告的每个 global; 5 个示例退出码 0 | sway 会话命令 + 原始输出 |
| 可维护性 | 无包级可变状态; 无 `init()` 副作用; 每个 goroutine 有停止条件; `gofmt` / `go vet` 零告警 | 命令 + 代码审查 |
| 可观测性 | 错误含操作 / interface / 消息上下文; 活动对象事件不静默丢弃; destroyPending 与已取消 roundtrip 的丢弃按状态机显式记录; 错误路径有测试断言 | 测试 + 代码审查 |
| 性能 | 不适用: 意图未设性能目标; L4 记录基线, 不作为 v0.1.0 门禁 | 不适用 |

## 8. 明确不做的

引用 `docs/charter.md` 的非目标, 原文不改写:

- 不做 Wayland 服务端: 不实现 compositor, 不接受客户端连接, 不生成服务端绑定.
- 不引入 CGO, 不链接 libwayland: 库与示例的构建不出现 cgo, 运行时不依赖 libwayland.so; 不调用 wayland-scanner 或其他 C 实现.
- 不支持 X11/XWayland: 不读取 `DISPLAY`, 不调用 Xlib 或 xcb.
- 不做 GPU 与显示后端: 不实现 EGL, OpenGL, Vulkan, DRM, GBM, 不提供渲染 API; 68 个协议的绑定照常生成, 但 5 个示例只用 `wl_shm` 缓冲.
- 不做高层 UI: 不提供窗口管理, 控件, 布局, 主题; 5 个示例直接调用协议, 只创建各自需要的最小窗口.
- 不提供运行时协议加载: 不在程序启动时解析 XML, 不做插件式协议; 绑定在开发期生成并提交.
- 不声称未被 sway 广告的协议在运行时可用: 交付说明按协议标注 "真实连接已验证" 或 "仅绑定与编解码已验证".

其余 Out of Scope 见 `docs/charter.md`: 其他 OS / 架构, 大端, 构建标签; 第三方 Go 依赖; 68 XML 与 5 示例之外的新协议 / 新示例; 上游协议自动同步; 性能 / 吞吐 SLA.

## 9. 未决问题

| ID | 问题 | 影响 | 关闭责任人 | 截止 |
| :--- | :--- | :--- | :--- | :--- |
| 无 | 无 | 无 | 无 | 无 |

公共 API 名称, 包内布局与内部结构由 L3 契约定, 属于有意放行, 不是未决问题. 反向检查发现的新问题必须在 G2 前关闭或降级为 6.3 的已知限制.

## 10. 验收标准

> 证据命令中的测试名与示例路径是实现目标; L3 / L4 可以调整名称, AC 的 Given / When / Then 语义不变. 集成测试使用 `-tags=integration`; 先决条件是 `WAYLAND_DISPLAY=wayland-1` 的 sway 会话.

### 10.1 生成器

| AC | Given | When | Then | 判定 | 证据 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| AC-G01 | 干净工作区; 68 个 XML 可读 | 在仓库根连续执行两次 `go generate ./...` | 第二次后 `git diff --exit-code` 返回 0; 生成文件不含时间戳与本机绝对路径 | 命令 | `go generate ./... && go generate ./... && git diff --exit-code && go test ./... -run TestGeneratedNoHostPaths -v` |
| AC-G02 | 测试用无效 XML / DTD 目录 | 运行生成器 | 非 0 退出; 错误含文件与位置; 输出目录无变化 | 单元测试 | `go test ./... -run TestGeneratorRejectsInvalidInput -v` |
| AC-G03 | 测试用语义无效模型: since 越界 / 引用未解析 / 跨协议重名无法按 stable 优先解析 / opcode 不连续 / enum 重复 / 非法 allow-null | 运行生成器 | 非 0 退出; 错误含文件与字段; 输出目录无变化 | 单元测试 | `go test ./... -run TestGeneratorRejectsInvalidModel -v` |
| AC-G04 | 68 个 XML | 解析并生成元数据 | interface / version / request / event / opcode / 参数类型 / 引用目标 / enum 名称与取值, 以及 since / deprecatedSince / allowNull / frozen / bitfield / enum entry since 逐项与 XML 源一致, 差异 0 | 单元测试 | `go test ./... -run TestGeneratorMetadata -v` |
| AC-G05 | 生成结果已提交 | 构建全部包并检查布局 | `go build ./...` 退出 0; `go test ./... -run TestGeneratedLayout -v` 通过 | 命令 | `go build ./... && go test ./... -run TestGeneratedLayout -v` |
| AC-G06 | 输出目录不可写 / 磁盘错误 / 非法 flag | 运行生成器 | 非 0 退出; 原子写入, 无半成品 | 单元测试 | `go test ./... -run TestGeneratorWriteError -v` |
| AC-G07 | 68 个 XML | 用 wayland.dtd 逐个校验 | 68/68 通过; 退出码 0 | 命令 | `for f in $(find wayland-protocols -name '*.xml'); do xmllint --noout --dtdvalid wayland-protocols/wayland.dtd "$f"; if [ $? -ne 0 ]; then exit 1; fi; done` |

### 10.2 连接与关闭

| AC | Given | When | Then | 判定 | 证据 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| AC-C01 | sway 会话; `WAYLAND_SOCKET` 有效 fd, 或 `WAYLAND_DISPLAY` 名字/绝对路径, 或 `wayland-0` 存在 | 连接; 连续两次 Close; 两个 goroutine 并发 Close | 连接成功; 重复 Close 返回 nil; 并发 Close 串行化; 继承 fd 在 Close 后关闭; 不 panic | 集成测试 | `go test -tags=integration ./... -run TestConnect -v` |
| AC-C02 | 未设置 `WAYLAND_DISPLAY` / `WAYLAND_SOCKET`, 或 fd 非法 | 连接 | 返回 E-01 或 E-02; 不 panic | 单元测试 | `env -u WAYLAND_DISPLAY -u WAYLAND_SOCKET go test ./... -run TestConnectErrors -v` |
| AC-C03 | 已 Close 的连接 | Send / Dispatch / Roundtrip | 返回 E-03; 不写入 socket | 单元测试 | `go test ./... -run TestClosed -v` |

### 10.3 线协议

| AC | Given | When | Then | 判定 | 证据 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| AC-W01 | 测试参数: int / uint / fixed (-1.5, 0, 1.25) / string (null 0, 空 1, 非空含 NUL) / array (null / 空 / 非空) / fd / new_id / object, 含 allow-null | 编码后解码 | 值相等; 字节对齐; 主机小端 | 单元测试 | `go test ./... -run TestWireRoundtrip -v` |
| AC-W02 | 68 个 XML 的全部 request / event | 生成编解码用例并执行 | 每条消息至少 1 个用例; 不匹配数为 0 | 单元测试 | `go test ./... -run TestWireCoverage -v` |
| AC-W03 | 内存中的截断消息 / 长度小于 8 / 对齐错误 | 解码 | 返回 E-06; 不 panic | 单元测试 | `go test ./... -run TestWireMalformed -v` |
| AC-W04 | socketpair; 一条消息含 2 个 fd; 另一条含 1 个 fd; 再构造一个未知对象事件 | 发送 / 接收 / 跳过 | fd 顺序保持; 接收 fd 归调用方; 库不关闭发送 fd; 跳过未知事件的接收 fd 由库关闭; MSG_CTRUNC 返回 E-06; fd 消息部分写用 sendmsg 重试同一 ancillary | 单元测试 | `go test ./... -run TestWireFD -v` |
| AC-W05 | 非 allow-null 的 object / new_id 为 0, 或 string / array 为非法 null | 编码或发送 | 返回 E-10; 不写入 socket | 单元测试 | `go test ./... -run TestWireInvalidNull -v` |
| AC-W06 | fd 为负或无效 | 编码或发送 | 返回 E-10; 不写入 socket | 单元测试 | `go test ./... -run TestWireInvalidFD -v` |
| AC-W07 | 单条消息编码后长度大于 65535 | 编码 | 返回 E-06; 不写入 socket | 单元测试 | `go test ./... -run TestWireOversize -v` |

### 10.4 对象与 ID

| AC | Given | When | Then | 判定 | 证据 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| AC-O01 | 已连接对象表 | 分配 / 销毁 / 收到 delete_id / 再分配 | ID 在客户端范围; delete_id 后可复用; 0 不作对象 | 单元测试 | `go test ./... -run TestObjectLifecycle -v` |
| AC-O02 | destroyPending 或 destroyed 对象 | 发送请求 | 返回 E-11; 不写入 socket | 单元测试 | `go test ./... -run TestDestroyedSend -v` |
| AC-O03 | 事件对象不在表中, 或 opcode 超出范围 | 分发 | 返回 E-07; 连接转 closed | 单元测试 | `go test ./... -run TestUnknownObject -v` |
| AC-O04 | 客户端对象 ID 范围耗尽 | 分配新对象 | 返回 E-16; 不发送 | 单元测试 | `go test ./... -run TestObjectIDExhausted -v` |

### 10.5 事件分发与并发

| AC | Given | When | Then | 判定 | 证据 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| AC-D01 | 已连接; 回调中发请求 | Dispatch 收到事件 | 回调串行执行; 回调在 Dispatch 调用者 goroutine 同步执行; 回调中的请求成功; 无 deadlock | 单元测试 | `go test -race ./... -run TestDispatchSerial -v` |
| AC-D02 | 8 个 goroutine 同时发请求 | 并发发送 | `-race` 通过; 每条消息原子写入; 同一 goroutine 内顺序保持 | 并发测试 | `go test -race ./... -run TestConcurrentSend -v` |
| AC-D03 | Dispatch 阻塞在空连接 | 另一个 goroutine Close | Dispatch 返回 E-03; 不 panic | 并发测试 | `go test -race ./... -run TestCloseUnblocksDispatch -v` |
| AC-D04 | 连接收到 protocol error | Dispatch | 返回 E-05; 后续 Dispatch 返回 E-03; 回调不再执行 | 单元测试 | `go test ./... -run TestDispatchProtocolError -v` |
| AC-D05 | 已连接; 两个 goroutine 同时调用 Dispatch 或 Roundtrip | 并发调用 | 调用被串行化; 回调不重叠; 无 deadlock | 并发测试 | `go test -race ./... -run TestConcurrentDispatch -v` |

### 10.6 版本协商

| AC | Given | When | Then | 判定 | 证据 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| AC-V01 | 调用方持有 Global 广告版本 5; interface 版本 7 | 请求 bind version 6 | 返回 E-08; 不发送 bind | 单元测试 | `go test ./... -run TestBindVersion -v` |
| AC-V02 | 对象版本 3; 请求 since 4 | 发送请求 | 返回 E-09; 不写入 socket | 单元测试 | `go test ./... -run TestSinceGuard -v` |
| AC-V03 | sway 广告的 global 表 | bind 广告版本; 读取 global / global_remove | 绑定成功; 事件按顺序; 重复 bind 创建新对象 | 集成测试 | `go test -tags=integration ./... -run TestBindVersions -v` |
| AC-V04 | `wl_registry.bind` 的 new_id 无 interface; interface 名在 68 个绑定中 | bind | 创建的对象 interface 与名称一致; 对象 version 等于 bind 请求 version | 单元测试 | `go test ./... -run TestDynamicNewID -v` |
| AC-V05 | 动态 new_id 的 interface 名不在 68 个绑定中 | bind | 返回 E-15; 不发送 | 单元测试 | `go test ./... -run TestDynamicNewIDUnknown -v` |

### 10.7 阻塞, 往返与 context

| AC | Given | When | Then | 判定 | 证据 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| AC-R01 | 已连接; 先发一个请求再 Roundtrip | Dispatch / Roundtrip | Dispatch 阻塞到事件; Roundtrip 在 sync done 且先前事件交付后返回 | 集成测试 | `go test -tags=integration ./... -run TestRoundtrip -v` |
| AC-R02 | 已连接 | Roundtrip 使用已取消或超时的 context | 返回 `context.Canceled` / `context.DeadlineExceeded`; 连接仍可用; 后续 Dispatch / Roundtrip 成功; 被取消的 callback 不交付 | 单元测试 | `go test ./... -run TestRoundtripCancel -v` |
| AC-R03 | socket 写缓冲已满 | Send 阻塞; 另一个 goroutine Close | Send 返回 E-03 或 E-04; 不 deadlock | 并发测试 | `go test -race ./... -run TestSendBlocking -v` |

### 10.8 错误模型

| AC | Given | When | Then | 判定 | 证据 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| AC-E01 | 测试服务端发送 `wl_display.error` | Dispatch | 返回 E-05; 含 objectID / interface / code / message; 连接 closed | 单元测试 | `go test ./... -run TestProtocolError -v` |
| AC-E02 | 测试服务端关闭连接 | Dispatch / Send | 返回 E-04; 不 panic | 单元测试 | `go test ./... -run TestIOError -v` |
| AC-E03 | 各类错误 | `errors.Is` / `errors.As` | 能区分 E-01 到 E-16; 错误消息含操作 / interface / 消息上下文; 不依赖字符串 | 单元测试 | `go test ./... -run TestErrorCategories -v` |

### 10.9 示例

| AC | Given | When | Then | 判定 | 证据 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| AC-X01 | sway 会话 | 运行 registry 示例 | 退出码 0; stdout 含 `wl_compositor`, `wl_shm`, `xdg_wm_base`; global 数大于 0 | 端到端 | `go run ./examples/registry` |
| AC-X02 | sway 会话 | 运行 shm-surface 示例 | 退出码 0; 显示 200x200 纯色窗口, 1 秒后退出 | 端到端 | `go run ./examples/shm-surface` |
| AC-X03 | sway 会话 | 运行 xdg-shell 示例; 按 Esc 或关闭 | 退出码 0; 400x300 窗口, 标题 `wayland-go demo` | 端到端 | `go run ./examples/xdg-shell` |
| AC-X04 | sway 会话 | 运行 input 示例; 按 Esc | 退出码 0; stdout 有原始 evdev keycode 与 wl_fixed 指针坐标 | 端到端 | `go run ./examples/input` |
| AC-X05 | sway 会话 | 运行 output 示例 | 退出码 0; 每个显示器输出 name / 分辨率 / scale; 数量大于 0 | 端到端 | `go run ./examples/output` |
| AC-X06 | 5 个示例; 未设置 `WAYLAND_DISPLAY` / `WAYLAND_SOCKET` | 逐个运行 | 每个示例非 0 退出; stderr 含连接来源错误与 WAYLAND 变量提示 | 端到端 | `bad=0; for e in registry shm-surface xdg-shell input output; do env -u WAYLAND_DISPLAY -u WAYLAND_SOCKET go run ./examples/$e; if [ $? -eq 0 ]; then bad=1; fi; done; test $bad -eq 0` |
| AC-X07 | 测试服务端不发送 global | 运行 registry 示例 | 非 0 退出; stderr 报无 global | 单元 / 端到端 | `go test ./... -run TestRegistryNoGlobals -v` |

### 10.10 registry-bind

| AC | Given | When | Then | 判定 | 证据 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| AC-RB01 | sway 会话, 已知与未知接口的 global 都在广告 | 对每个 global 按广告版本 bind; 有绑定用 typed, 未知用测试内 raw; 跑一次 roundtrip | 每个 bind 成功; roundtrip 返回; 无 protocol error; 退出码 0 | 集成测试 | `go test -tags=integration ./... -run TestRegistryBind -v` |
| AC-RB02 | 未知接口的 global | 收到该对象的事件 | 按键长跳过; 不 panic; 连接仍可用 | 集成测试 | `go test -tags=integration ./... -run TestRegistryBindUnknownEvents -v` |
| AC-RB03 | 测试服务端对某个 bind 返回 error | 运行 registry-bind | 测试失败并报告 interface / version / error; 不 hang | 单元测试 | `go test ./... -run TestRegistryBindError -v` |
