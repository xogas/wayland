# G2 反向检查: wayland (2026-11-15)

- **对象**: `docs/spec.md` (检查时 v0.1 Draft; 定稿 v1.0 Frozen)
- **方法**: 独立 subagent 只读 `docs/spec.md`, 不看其他项目文件或对话; 输出实现计划与它需要自行决定的点.
- **第一轮结论**: 17 个真实 gap, 8 个矛盾 / 缺失 / 歧义, 10 个矩阵格需要处理; 修订后的 spec 已逐条处理.
- **修订后自检**: 47 条 AC; 每个功能至少 1 条正向 + 1 条异常; 异常矩阵无空格; 未决问题 0.
- **G2 状态**: 2026-11-15 用户确认通过; spec 冻结为 v1.0 Frozen.

## AI 实现计划摘要

- 根包: Connect / Close / Send / Dispatch / Roundtrip; 对象表; bind 与版本 / since 校验; 错误类型; registry 事件.
- 线协议: 头与 8 种参数编解码; 对齐; fd ancillary; 每消息 marshal / unmarshal; 覆盖表.
- 生成器: 读取 XML; DTD; 模型; 语义不变量; 渲染; 原子替换; 确定性.
- 生成包: 根包 + `protocols/`; 请求方法; 事件解码; interface / version / enum 元数据.
- 示例: 5 个 main; 测试资产: fake server, socketpair, 测试内 raw bind, 未知事件跳过.
- 错误: E-01 到 E-16 的类型映射与 `errors.Is` / `errors.As`.

## 有意放行 (L3)

| 项 | 处理 |
| :--- | :--- |
| 导出标识符与签名 | L3 契约; spec 只锁行为 |
| 包布局与 import 方向 | L3 契约; 根包 / protocols 方向在 L3 冻结 |
| 生成符号形状 | L3 契约; spec 锁元数据与行为 |
| 错误类型名与允许 panic 的 nil 参数 | L3 契约; spec 锁语义与 panic 边界 |
| 对象表 / ID 分配 / 日志的内部实现 | L3 契约; spec 只锁可观察行为 |
| go:generate 指令位置与生成器 flag 名 | L3 契约; L2 只锁 `go generate ./...` 入口 |

## 真实 gap 与处理

| 编号 | 发现 | 处理 |
| :--- | :--- | :--- |
| G1 | bind 需要广告版本, 但库不缓存 global | spec 3 / 4.4: bind 输入是调用方持有的 Global; 校验 1..min(Global.version, interface.version); AC-V01 |
| G2 | 动态 new_id 的版本来源冲突 | spec 3 / AC-V04: 对象 version 等于 bind 请求 version, 不自动取广告版本 |
| G3 | 并发 Dispatch / Roundtrip 是否允许 | spec 4.1: 同一时刻一个; 其他调用等待; AC-D05 |
| G4 | 回调 goroutine 亲和性 | spec 4.1 / 6.3: 回调在 Dispatch / Roundtrip 调用者 goroutine 同步执行; AC-D01 |
| G5 | Close 错误与并发 Close | spec 4.1: 第一次返回关闭错误, 后续 nil; 并发串行化; AC-C01 |
| G6 | Roundtrip 取消后的读取与回调 | spec 4.3: 连接可用; 被取消 callback 事件丢弃; 不影响其他事件; AC-R02 |
| G7 | fd 数量 / 顺序 / 跳过事件 / MSG_CTRUNC | spec 3 / 6.3 / AC-W04: 顺序保持; 接收 fd 归调用方; 跳过事件 fd 由库关闭; 截断 E-06 |
| G8 | alive / 重复 / 未知 delete_id | spec 3 / 4.2: 均为 E-07; 只在 destroyPending / destroyed 合法; ID 只在 delete_id 后复用 |
| G9 | 错误时是否向服务端发 wl_display.error | spec 5: E-06 / E-07 只本地关闭, 不向服务端发送 |
| G10 | WAYLAND_SOCKET 所有权, 绝对路径, wayland-0 | spec 4.1: 继承 fd 由库关闭; 绝对路径直接使用; 缺省 wayland-0; AC-C01 / C02 |
| G11 | 生成器输入少于 / 多于 68 | spec 6.1: 处理根目录输入集合; 空集 E-13; v0.1.0 的 68 个文件是验收基线; 不硬编码 68 |
| G12 | 只有 registry 测无显示 | AC-X06: 5 个示例逐个测无显示退出 |
| G13 | input 示例的 keycode 约定 | spec 6.3 / AC-X04: 原始 evdev keycode, Esc 为 1; wl_fixed 十进制 |
| G14 | registry 事件顺序 | spec 4.4: 按服务端到达顺序, 不按名称排序 |
| G15 | fixed 24.8 转换 | spec 3 / AC-W01: 保持 32 位值; 测试 -1.5 / 0 / 1.25 |
| G16 | deprecatedSince / frozen / bitfield 元数据 | AC-G04: 与 XML 逐项比较 |
| G17 | 测试服务端位置与 API | spec 6.3: 测试资产, 不进公共 API; 位置 L3 / L4 |

## 矛盾与歧义

| 编号 | 发现 | 处理 |
| :--- | :--- | :--- |
| C1 | 事件不静默丢弃 vs destroyPending / 取消 callback 丢弃 | spec 4.2 / 7: 这两类丢弃是状态机显式行为, 不是静默丢弃 |
| C2 | 连接初始化失败归 E-01 / E-02 vs E-04 | 6.2 改为连接建立后 IO 失败 E-04 (AC-E02) |
| C3 | delete_id 与 alive -> destroyed 冲突 | spec 3 / 4.2: alive 对象收到 delete_id 为 E-07 |
| C4 | 动态 new_id 版本按广告版本 vs bind 请求版本 | AC-V04 改为 bind 请求 version |
| C5 | 输入 1..68 vs 固定 68 | spec 6.1: 输入集合由根目录决定; 68 是验收基线 |
| C6 | string 0 长度空 vs NUL 长度 | spec 3: null string 0, 空 string 1, 非空含 NUL; array 0 为 null / 空 |
| C7 | 解码长度大于 65535 不可能 | AC-W03 只管解码; 新增 AC-W07 管编码超限 |
| C8 | EOF 与内存截断 | spec 5: 消息中间 EOF E-04; 内存完整帧格式错误 E-06 |

## 缺失 AC

| 项 | 处理 |
| :--- | :--- |
| M1 DTD 正向校验 | 新增 AC-G07; 68/68 |
| M2 错误类别覆盖 | AC-E03 扩到 E-01 到 E-16 |
| M3 元数据字段 | AC-G04 加入 since / deprecatedSince / allowNull / frozen / bitfield / enum entry since |
| M4 5 个示例无显示 | AC-X06 覆盖 5 个 |
| M5 生成输出无时间戳 / 本机路径 | AC-G01 + 6.3 |
| M6 错误上下文 | AC-E03 要求操作 / interface / message 上下文 |
| M7 无 global | 新增 AC-X07 |
| M8 WAYLAND_SOCKET / wayland-0 | AC-C01 / C02 |

## 矩阵格处理

- B1 连接初始化失败: 改为 E-04 / AC-E02.
- B2 delete_id: 3 / 4.2 明确 alive / 重复 / 未知均为 E-07.
- B3 global_remove 后 bind: N/A, 库不缓存; 调用方决定; 失败 E-08 / E-05.
- B4 输入基数: 6.1 改为根目录输入集合, 68 是基线.
- B5 示例失败隔离: N/A, 每个示例独立进程; 单独验证.
- B6 fd 部分写: 6.1 / AC-W04 明确 sendmsg 原子重试.
- B7 空 / null string: 3 明确 0 / 1 / NUL.
- B8 短读 / EOF: 5 区分 E-04 与 E-06.
- B9 重复 bind: 4.2 / 4.4 每次新对象, 不自动销毁旧对象.
- B10 输出目录缺失: 6.1 / AC-G06 生成器创建目录; 不可写 E-14.

## 结论

第一轮反向检查发现的真实 gap 已写入 spec 或明确列为 L3 有意放行; 没有剩余 "只能猜" 的行为点.

## 用户确认

- 日期: 2026-11-15.
- 结论: G2 通过.
- 确认内容: 跨协议重名 stable 优先; registry-bind 测试内 raw bind; spec 冻结为 v1.0 Frozen.
