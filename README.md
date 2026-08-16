# DSH Desktop（DSH.exe）

把 `npx @deepseek-ai/dsh web` + 浏览器打开 `http://127.0.0.1:3080` 两步，封装成
一个桌面应用：**双击 `DSH.exe` 即自动启动服务并弹出内嵌窗口**，无需再敲命令。

## 两种版本，按需分发

| | `DSH.exe`（精简版） | `DSH-full.exe`（完整版，推荐分发给其他人） |
| --- | --- | --- |
| 体积 | ~5 MB | ~112 MB |
| 目标电脑要求 | 需已安装 Node.js | **零依赖**：Node.js + dsh 运行时已内置 |
| 首次启动 | 秒开（找不到 dsh 组件时自动 npm 安装） | 自动解压内置运行时（约 1-2 分钟），之后秒开 |
| 适用场景 | 开发机、自己用（本来就是 dsh 用户，必有 Node） | 发给任何 Windows 10/11 x64 电脑，无需装任何东西 |

两者功能完全一致：内嵌窗口、托盘、开机自启、单实例、端口自动分配、进程清理。

> `DSH-full.exe` 会把内置的 Node.js（v24）和 `@deepseek-ai/dsh` 解压到
> `%LOCALAPPDATA%\DSH\runtime\`。若系统已装 Node，完整版仍优先使用内置运行时，
> 保证行为一致、版本可控。

## 使用

1. 双击 `DSH.exe`（或 `DSH-full.exe`）。
2. 启动时自动检测 Node.js：
   - **已安装** → 直接启动服务并弹出内嵌窗口；
   - **未安装** → 窗口内显示引导页，点击「打开 Node.js 下载页」安装后，
     再点「我已安装，重启服务」即可自动重新检测并启动（无需重开程序）。
3. 托盘图标右键可：重新打开窗口、重启服务、检查更新、开关开机自启、退出。
4. 关闭窗口 = 最小化到托盘，服务继续在后台运行；托盘「退出」才会停止服务。

> 若内嵌浏览器不可用（例如 WebView2 运行时缺失），会自动降级为用系统默认浏览器打开。

## 关键设计

- **端口不冲突**：服务用 `--port 0` 启动，系统自动分配空闲端口，应用解析输出
  里的 URL 并打开，任何机器上都不会撞端口。
- **不需要安装**：单文件便携 exe，不写系统目录、不需要管理员权限、不装服务。
  完整版连 Node.js 都不需要。
- **进程清理**：退出时用 job object + taskkill + TerminateProcess 三重保险
  清理整个 node 进程树，崩溃也不留孤儿进程。
- **启动失败自动修复**：若服务器启动即崩溃（dsh 组件损坏/安装不完整），应用会
  立即显示错误原因，并自动删除损坏的 dsh 组件、重新安装后重启（全程有进度提示，
  不会无限卡在「正在启动」）。
- **单实例**：重复双击只聚焦已打开的窗口。
- **图标**：`icon.png` 是应用图标源文件（256×256），构建时经 `winres.json`
  生成 exe 文件图标，并由 `tools/make-icon.ps1` 生成 `icon.ico` 供窗口和托盘使用。

## 环境要求

- Windows 10 / 11（64 位）
- 精简版需要 Node.js（完整版不需要）

## 目录结构

| 文件 | 说明 |
| --- | --- |
| `DSH.exe` | 精简版成品 |
| `DSH-full.exe` | 完整版成品（内置运行时） |
| `main.go` `app.go` `server.go` `ui.go` `tray.go` `runtime*.go` `resources.go` | 源码 |
| `runtime.zip` | 内置运行时压缩包（Node.js + dsh，仅完整版使用；构建产物，不随仓库提交） |
| `third_party/go-webview2/` | 打了补丁的 WebView2 封装库（修复现代运行时回调签名兼容 + 优雅失败） |
| `third_party/dsh-runtime/` | 运行时源目录（node + dsh 依赖；node_modules 产物，不随仓库提交，按下方步骤重新生成） |
| `vendor/` | Go 依赖快照，可离线编译 |
| `icon.ico` `icon.png` | 应用图标（icon.png 为设计源文件） |
| `winres.json` `rsrc_windows_amd64.syso` | Windows 资源（图标/清单/版本信息） |
| `tools/make-icon.ps1` | 图标生成脚本 |
| `tools/mkzip/` | 运行时压缩打包工具 |
| `build.ps1` | 一键构建脚本 |

## 重新构建

```powershell
cd G:\ad-dsh\dsh-desktop
.\build.ps1            # 精简版 DSH.exe
.\build.ps1 -Full      # 完整版 DSH-full.exe（需先有 runtime.zip）
```

重新打包内置运行时（更新 dsh 版本后，`third_party\dsh-runtime` 未随仓库提交，需先重建）：

```powershell
# 1) 准备 Node.js（win-x64）：从 https://nodejs.org/dist/ 下载
#    node-v24.x.x-win-x64.zip，解压到 third_party\dsh-runtime\node
# 2) 安装 dsh 到 third_party\dsh-runtime\dsh：
.\third_party\dsh-runtime\node\node.exe .\third_party\dsh-runtime\node\node_modules\npm\bin\npm-cli.js install @deepseek-ai/dsh --prefix .\third_party\dsh-runtime\dsh
#    （可选：删除非 win32-x64 平台的二进制以减小体积，如 @img/sharp-*、node-pty/prebuilds 中的其他平台目录）
# 3) 打包并构建完整版：
go run ./tools/mkzip -src third_party\dsh-runtime -out runtime.zip
.\build.ps1 -Full
```

需要本机有 Go 工具链（或用 `$env:DSH_GO` 指定 go.exe 路径）。若环境变量
`HTTPS_PROXY` 未设置且无法直连，可先 `$env:HTTPS_PROXY="http://127.0.0.1:10809"`
再构建（Go 模块代理走该代理）。

## 运行时目录与日志

- 数据目录：`%LOCALAPPDATA%\DSH\`（内置运行时、dsh 组件、WebView2 用户数据）
- 日志：`%LOCALAPPDATA%\DSH\logs\app.log`、`server-YYYYMMDD.log`

## 测试 / 调试用环境变量（可选）

| 变量 | 作用 |
| --- | --- |
| `DSH_DEBUG=1` | 启用 WebView2 开发者工具和右键菜单 |
| `DSH_ROOT=路径` | 覆盖数据根目录 |
| `DSH_LOG_DIR=路径` | 覆盖日志目录 |
| `DSH_WEBVIEW2_DATAPATH=路径` | 覆盖 WebView2 用户数据目录 |
| `DSH_AUTO_QUIT_MS=毫秒` | 启动 N 毫秒后自动退出（自动化测试用） |
| `DSH_FORCE_NODE_MISSING=1` | 模拟未安装 Node.js，触发窗口内安装引导页（测试用） |
| `DSH_DISABLE_AUTO_REPAIR=1` | 关闭「启动失败自动重装 dsh」逻辑（测试用） |

## 已知说明

- 本机若检测不到 WebView2 运行时，会弹窗提示并自动改用系统浏览器打开。
- 沙箱/受限环境（如部分企业安全软件）可能阻止 WebView2 或进程清理，
  正常桌面环境下无此问题。
- 分发完整版时请一并保留 `LICENSE` 相关说明（Node.js 与 npm 均为 MIT 许可，
  各 npm 包的许可文件已随 `third_party/dsh-runtime` 保留在解压产物中）。
