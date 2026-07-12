# Wayland Go Binding Examples

本目录包含多个独立可运行的 Wayland 客户端示例, 演示 github.com/xogas/wayland 库的各种用法.

## 运行要求

- Linux 系统, 运行中的 Wayland compositor (KDE/KWin, GNOME/Mutter, Sway, Weston 等)
- XDG_RUNTIME_DIR 环境变量已设置
- Go 1.26+

## 基础示例

### globals -- 全局接口枚举

连接 Wayland compositor, 获取所有 global 接口并打印名称与版本号.

```sh
go run ./example/globals
```

### outputs -- 显示器信息与热插拔监听

绑定所有 wl_output, 打印每个显示器的名称、分辨率、刷新率、缩放系数; 然后进入 5 秒动态监听, 期间热插拔显示器会实时打印新增/移除事件.

```sh
go run ./example/outputs
```

### helloworld -- 最简窗口 + 内嵌字体

使用 xdg-shell 创建 toplevel 窗口, 内嵌 5x7 点阵字体, 在共享内存缓冲区中渲染 "Hello, Wayland!" 文字. 窗口尺寸根据文字内容自适应.

```sh
go run ./example/helloworld
```

无交互按键, 30 秒超时自动退出.

## 渲染示例

### simpleshm -- 双缓冲动画

250x250 窗口, 绘制随时间变化的同心圆环动画. 演示双缓冲 (2 个 shm buffer + wl_buffer release 跟踪) 与 wl_surface.frame 回调驱动的动画循环.

```sh
go run ./example/simpleshm
```

无交互按键, 60 秒超时自动退出, 退出时打印帧率统计.

### damage -- 增量 Damage 演示

400x400 窗口, 小方块沿圆形轨迹移动. 使用 wl_surface.damage 或 damage_buffer (根据 compositor 版本) 仅刷新脏区域, 计算并打印实际 damage 面积占比.

```sh
go run ./example/damage
```

按 D 键切换增量 damage / 全量 damage 模式, 终端可见比率为 0.007 vs 1.0.

### viewport -- wp_viewporter 裁剪与缩放

512x512 大缓冲区, 通过 wp_viewport 设定 256x256 源矩形旋转描画区, 目标尺寸可调. 演示不重新 attach buffer 即改变显示区域.

```sh
go run ./example/viewport
```

空格暂停/恢复; -/= 缩小/放大目标尺寸.

### presentation -- presentation-time 反馈

256x256 窗口, 移动方块动画. 每帧通过 wp_presentation_feedback 获取呈现时间戳, 计算并打印 commit-to-present 延迟统计数据 (avg/min/max).

```sh
go run ./example/presentation
```

无交互按键, 每 60 帧打印一次延迟报告.

### cube -- 软件渲染旋转 3D 立方体

480x480 窗口, 纯 CPU 渲染: 透视投影、背面剔除、画家算法排序、扫描线三角形光栅化. 双缓冲 + frame 回调驱动.

```sh
go run ./example/cube
```

无交互按键, 60 秒超时, 退出时打印帧率.

## 输入示例

### eventdemo -- 统一输入事件查看器

640x260 窗口, 内嵌完整 ASCII 32-126 字体. 实时打印 wl_keyboard (keymap/enter/leave/key/modifiers/repeat)、wl_pointer (enter/leave/motion/button/axis/wheel) 和 wl_touch (down/up/motion) 全部事件. 每次事件触发即重绘整个缓冲区 (演示简单实现, 非性能优化).

```sh
go run ./example/eventdemo
```

无交互按键, 60 秒超时, 使用输入设备即可看到窗口与终端的事件输出.

### cursor -- 光标形状演示

400x300 窗口, 支持两种光标模式: (A) 自绘十字光标 surface; (B) 通过 cursor-shape-v1 协议使用 compositor 内置光标.

```sh
go run ./example/cursor
```

1 切换到自绘光标模式 (A); 2 切换到 cursor-shape-v1 模式 (B); 在模式 B 中, 左/右方向键循环形状: default, pointer, crosshair, text, move, grab.

### smoke -- 烟雾流体模拟

400x400 窗口, Jos Stam 流体求解器. 鼠标移动搅动烟雾, 无操作时自动周期注入随机烟团.

```sh
go run ./example/smoke
```

鼠标移动搅动流体 (无点击), 60 秒超时.

## 窗口管理示例

### resizor -- 交互式窗口管理

窗口支持指针拖拽移动/边缘拖拽缩放, 键盘切换最大化/全屏/最小化/手动调整大小. 绘制彩色边框指示当前状态 (activated/resizing/tiled 等). 可选 zxdg_decoration_manager_v1 请求 server-side decoration.

```sh
go run ./example/resizor
```

m 切换最大化; f 切换全屏; n 最小化; 上/下方向键增减窗口高度 (30px 步进); 鼠标左键拖拽窗口移动, 边缘拖拽窗口缩放; q 退出. 120 秒超时.

### popup -- 右键菜单 + xdg_positioner

400x300 窗口, 右键弹出自绘 160x100 三色菜单 (Item 1/2/3). 2 秒后自动弹出一个 popup, 3 秒后自动关闭. 演示 xdg_popup + xdg_positioner + grab 机制.

```sh
go run ./example/popup
```

右键弹出菜单; 左键点击菜单项选择 (终端打印选中项); 2 秒后自动弹窗演示.

### subsurfaces -- 子表面动画

400x400 主窗口 + 120x120 子表面, 子表面沿圆形轨迹移动, 演示 sync/desync 模式切换和 z-order 控制 (place_above/place_below).

```sh
go run ./example/subsurfaces
```

S 切换 sync/desync 模式; R 切换 place_above/place_below.

### activation -- xdg-activation 焦点转移

创建红蓝两个 300x200 窗口, 按 Tab 键通过 xdg_activation_v1 协议请求焦点在窗口间转移.

```sh
go run ./example/activation
```

Tab 键转移焦点 (当前焦点窗口 -> 另一窗口); 3 秒内无键盘输入则自动尝试激活窗口 B.

## 数据交换示例

### dnd -- 拖放与剪贴板

500x300 窗口, 显示 4 个彩色方块. 左键拖动方块触发 drag-and-drop (发送颜色值 application/x-color); c/v 键通过 wl_data_device 实现剪贴板复制粘贴.

```sh
go run ./example/dnd
```

左键拖拽彩色方块 (开始 drag-and-drop); c 复制 (set_selection); v 粘贴 (receive). 120 秒超时.

## 媒体示例

### imageviewer -- 图片查看器

用 Go 标准库解码 PNG/JPEG/GIF 并显示在窗口中; 超过 1600x1000 的图片按比例缩小.

```sh
go run ./example/imageviewer <image-file>
```

无交互按键, 60 秒超时自动退出.
