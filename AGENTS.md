# wayland 开发规则

## 项目定位

纯 Go 的 Wayland 客户端库, 外加一个从仓库内协议 XML 生成 Go 绑定的 cmd; 给库作者在 Linux 桌面 (sway) 写截图, 启动器一类小工具用; 只跑 linux/amd64. 本文件是硬约束, 不是说明书; 理由和背景在 `docs/`.

当前阶段: 意图已冻结; 立项, 规格, 契约与设计尚未开始.

## 不可违反 (违反即回退)

- 技术栈: Go 1.27.4 (以 `go.mod` 的 `go` 指令为准); 只用标准库; 第三方 Go 依赖为 0; 不许开 CGO, 不许链接 libwayland; 不许改 `go` / `toolchain` 行; 不许用 `GOEXPERIMENT`.
- 实现依据: 只用 `wayland-protocols/` 的 XML, `wayland.dtd` 与公开协议文档; 不许调用 wayland-scanner; 不许把 C 实现当对照基准.
- 平台: 只支持 linux/amd64; 不添加其他平台的构建标签, 不做跨平台兜底.
- 字节序: Wayland 线协议按主机字节序; linux/amd64 为小端; 按小端编码, 不做网络字节序转换.
- 协议生成: 递归读取 `wayland-protocols/` 下全部 68 个 XML; 生成代码提交进仓库; 同一输入连续生成两次, 第二次后 `git diff --exit-code` 必须为 0.
- 布局: wayland.xml 的绑定在模块根包; 其余协议在 `protocols/` 下按 stable/staging/unstable/experimental 与协议目录生成; 生成器在 `cmd/`.
- 生成代码: 不许手改; 要改就改生成器并重新生成. 不许编辑 `wayland-protocols/` 的 XML 与 DTD; 上游升级时整体替换再生成.
- 并发: 允许调用方从不止一个 goroutine 同时发请求; 事件分发与核心状态由库内部串行处理; 回调不并发; 调用方不需要额外加锁. 每个 goroutine 必须有停止条件; 禁止包级可变状态与 `init()` 副作用.
- 非目标: 不做服务端/compositor, 不生成服务端绑定; 不碰 X11/XWayland; 不做 EGL/OpenGL/Vulkan/DRM/GBM 或渲染 API; 不做窗口管理/控件/布局/主题; 不在运行时解析 XML 或加载插件式协议.
- 验证声明: sway 未广告的协议, 不声称真实连接可用; 交付说明按协议标注验证层级.
- experimental: 跟随上游版本; 上游更新 XML 就重新生成; 不承诺 API 跨版本不变.
- 文档: `docs/intent.md` v1.0 Frozen 是意图基线; 改语义必须升版本并重新过门禁. `docs/` 归库作者; 改 spec/契约/ADR 前先问.
- 依赖: 当前约束是第三方 Go 依赖为 0; 任何新增依赖 (含 `go get`) 都必须先问, 并先改意图与重新过门禁; 不许自行改 `go.mod` / `go.sum`.
- 设计: 接口只在消费方定义, 单实现不抽接口; 禁止按层命名 (`domain`/`service`/`repository`/`controller`) 与 `utils`/`helpers`/`common` 杂物包; 库包不许 import `cmd/` 或 `examples/`; 根包与 `protocols/` 的跨包方向在契约冻结前必须问.
- 禁改: `wayland-protocols/` 的 XML/DTD, 手改生成代码, `docs/intent.md`, `go.mod`, `go.sum`, `AGENTS.md`, `Makefile`; 需要对应用户批准.
- 不确定时: 停下来问; 不许假定后继续, 不许"先写一版看看", 不许把"我假设..."留到收工.
- 冲突时: 当次指令 > 子目录 `AGENTS.md` > 本文件 > 用户全局; 具体化只能收紧, 不能放宽; 任何覆盖必须在收工写明"破了哪条 + 为什么 + 何时补".

## 开工协议 (四步做完, 才允许改第一个文件)

1. 读 `docs/intent.md` 与当前阶段文档; 复述本次切片做什么, 明确不做什么.
2. 过一遍六类问题: 目标 / 边界 / 数据 / 失败 / 依赖 / 验收; 把按现有材料答不上来的点列出来, 停下来等我回答.
3. 声明计划改动的文件清单, 逐条对照"禁改"和当前切片的允许范围; 新增包或接口时说明是否需要 ADR.
4. 信息不足又没人可问时: 停止, 列出问题; 不许给默认选择后继续.

## 收工协议 (交不出这些不算完成)

1. 改动文件清单 + 每个文件为什么改.
2. 验证证据: 命令 + 原始输出, 不许总结成"测试通过":
   - `go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./... && go mod tidy && git diff --exit-code -- go.mod go.sum`
   - 新增包或接口: 说明是否守住布局与契约.
   - 改了生成器: 重新生成两次, 第二次后 `git diff --exit-code` 为 0; 生成代码的变更随本次提交.
   - 改了协议绑定: 附 DTD 校验, 解析与生成元数据一致性, 语义不变量, 全签名编解码用例的原始输出; 不许引入 C 对照.
   - 涉及真实会话: 附 sway 实跑输出与退出码; 不许只贴单元测试.
3. 与 `docs/` 的偏差: 有就逐条写, 没有写 "无".
4. 新增未决问题.
5. 本次破例: 破了哪条 + 为什么 + 何时补; 无则写 "无".
6. 做完即停; 不许自动开始下一项, 不许顺手重构不相干代码, 不许顺手升级依赖.

## 指针

- 意图: `docs/intent.md` (v1.0 Frozen)
- 规格与 AC: `docs/spec.md` (未创建)
- 接口契约: `docs/contracts.md` (未创建)
- 架构决策: `docs/adr/` (未创建)
- 协议源: `wayland-protocols/`; 生成器: `cmd/` (待创建); 非核心生成代码: `protocols/` (待创建); 示例: `examples/` (待创建)
- 未覆盖之处: 按 "不确定时" 处理, 问.

## 提交规范

- 提交信息只用英文, Conventional Commits, 单行 `<type>: <subject>`; 祈使句, 小写开头, 不加句号, 不超过 72 字符.
- 一个切片一个 commit; 只 `git add <具体路径>`; 不许 `git add -A` 或 `git commit -am`.
- 不许 `--amend` 已推送的提交; 不许 `push --force` 到主分支.
- 不许提交 `bin/`, `*.test`, 密钥或令牌, 本机绝对路径, `*.local.md`.
- 生成代码是交付物, 要提交; 但只许通过生成器更新, 不许手改.
