# DSH Desktop（DSH.exe）

把 `npx @deepseek-ai/dsh web` + 浏览器打开 `http://127.0.0.1:3080` 两步，封装成
一个桌面应用：**双击 `DSH.exe` 即自动启动服务并弹出内嵌窗口**，无需再敲命令。

## 特性一览

- **双击即用**：自动启动服务并弹出内嵌窗口，无需任何命令行操作
- **端口不冲突**：服务用 `--port 0` 启动，系统自动分配空闲端口，任何机器上都不会撞端口
- **便携免安装**：单文件 exe，不写系统目录、不需要管理员权限、不装服务
- **进程清理**：退出时用 job object + taskkill + TerminateProcess 三重保险清理整个
  node 进程树，崩溃也不留孤儿进程
- **启动失败自动修复**：服务器启动即崩溃时（dsh 组件损坏/安装不完整），立即显示错误
  原因，自动删除损坏组件、重新安装后重启（全程有进度提示，不会卡在「正在启动」）
- **单实例**：重复双击只聚焦已打开的窗口
- **托盘驻留**：关闭窗口 = 最小化到托盘，服务继续后台运行；托盘支持重新打开窗口、
  重启服务、检查更新、开关开机自启
- **浏览器降级**：内嵌浏览器不可用（如 WebView2 运行时缺失）时，自动改用系统默认浏览器打开

## 版本选择与环境要求

仅支持 **Windows 10 / 11（64 位）**，按目标电脑分发两个版本：

| | `DSH.exe`（精简版） | `DSH-full.exe`（完整版，推荐分发给其他人） |
| --- | --- | --- |
| 体积 | ~5 MB | ~112 MB |
| 目标电脑要求 | **需已安装 Node.js** | **零依赖**：Node.js + dsh 运行时已内置 |
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

## 目录结构

### 源码与构建产物

| 文件 | 说明 |
| --- | --- |
| `DSH.exe` / `DSH-full.exe` | 精简版 / 完整版成品 |
| `main.go` | 程序入口：单实例互斥、DPI 感知、日志初始化、serve/webview/托盘消息泵 |
| `app.go` | 应用装配与状态机：把服务、UI、托盘串起来；按服务状态渲染引导页/错误页/就绪页，处理重启与退出 |
| `server.go` | dsh 服务管理：定位 Node 与 dsh（npx 缓存 / npm 全局）、`--port 0` 启动、解析输出 URL、进程树管理 |
| `ui.go` | 内嵌窗口页面渲染（引导页 / 错误页 / 加载壳） |
| `tray.go` | 托盘菜单、开机自启、单实例窗口聚焦、日志目录 |
| `runtime*.go` | 内置运行时（完整版）：runtime 目录定位与 zip 解压（`runtime_embedded.go` / `runtime_standalone.go` 为构建变体） |
| `resources.go` | 内嵌静态资源 |
| `plugins.go` | 内嵌插件安装（`ensureBundledPlugins`）与插件清单（见「插件系统」） |
| `updater.go` | GitHub 版本检查 / 下载 / 替换 / 重启（含代理、重试、版本比较） |
| `plugins_test.go` `updater_test.go` | 对应单元测试 |
| `build.ps1` | 一键构建脚本（`.\build.ps1` 精简版，`-Full` 完整版） |
| `runtime.zip` | 内置运行时压缩包（仅完整版使用；构建产物，不随仓库提交） |
| `third_party/` | 打补丁的第三方库：`go-webview2/`（修复回调签名兼容 + 优雅失败）、`dsh-runtime/`（运行时源目录，node_modules 产物不随仓库提交） |
| `vendor/` | Go 依赖快照，可离线编译 |
| `icon*.png` `icon.ico` `winres.json` `rsrc_windows_amd64.syso` | 应用图标（`icon.png` 为设计源文件）与 Windows 资源（图标/清单/版本信息） |
| `tools/make-icon.ps1` `tools/mkzip/` | 图标生成脚本、运行时压缩打包工具 |

### 插件目录

| 目录 | 角色 | 是否打包进 EXE |
| --- | --- | --- |
| `dsh-plug-in/<插件名>/` | **插件源工程**：插件的开发与维护都在这里进行（含各自的 README / 安装脚本 / 补丁脚本） | ❌ 不会（仅作为源码工程保留在仓库中） |
| `bundled-plugins/<插件名>/` | **发布副本**：需要随 EXE 分发的插件存放点，构建时被 `plugins.go` 的 `//go:embed bundled-plugins` 内嵌进 exe | ✅ 会 |

## 插件系统

DSH 桌面端插件 = **Cordis loader 插件**：一个零依赖、自包含的 npm 包，由
`plugins.go` 在 EXE 启动时安装到
`%USERPROFILE%\.dsh\profiles\web\node_modules\<插件名>\`，并在该 profile 的
`cordis.patch.yml` 中注册对应的 Loader insert 行后加载生效。

### 发布机制

EXE 启动时（`server.go` → `ensureBundledPlugins`）会把 `bundled-plugins/` 中的每个
插件安装到 `%USERPROFILE%\.dsh\profiles\web\node_modules\<插件名>\`，并在
`cordis.patch.yml` 中注册对应的 Loader insert 行。插件已是最新时跳过，按
`.dsh-digest` 摘要增量更新，**幂等**。

### 插件清单（登记表）

> ⚠️ **后续新增插件必须在本表中登记**，本文档即插件的登记清单。

| 插件（目录名） | Loader id | 作用 |
| --- | --- | --- |
| `dsh-local-plugins` | `local-plugin-manager` | **本地插件管理**：「设置 → 插件」新增「本地插件」标签页——输入/浏览插件目录即可安装（校验后复制到 `<profile>/node_modules/<name>` 并在 `cordis.patch.yml` 追加 managed insert 行），也可列出并一键移除已安装插件；内置（`@deepseek-ai/*`）插件与本管理器自身受保护 |
| `dsh-session-delete` | `session-delete` | **会话删除**：侧边栏会话「⋮」菜单新增「删除会话」——确认后停止（如运行中）→ 落盘日志 → 立即归档（行马上从侧边栏/搜索消失）→ 日志移入 `<DSH_HOME>/trash/<日期>/<会话id>/` 回收站；同时负责对 `dsh-client-ui-workspace` 已发布 bundle 打菜单补丁（锚点不匹配自动跳过） |
| `dsh-updater` | `updater` | **软件更新中继**：桌面端（`updater.go`）负责真实的 GitHub 版本检查 / 下载 / 替换 / 重启；本插件 `status()` 读取桌面端写入的 `%LOCALAPPDATA%/DSH/update/state.json` 显示当前/最新版本，`check()` / `apply()` 向 `request.json` 写请求由桌面端轮询消费，UI 在「设置」中展示 |

### 新增插件工作流

**规则**：任何新增插件都必须在 `dsh-plug-in/` 中建源工程、在 `bundled-plugins/` 中
放发布副本、在 `plugins.go` 的 `bundledPlugins` 中登记，并在上方插件清单表补一行说明。

1. **建源工程**：在 `dsh-plug-in/<插件名>/` 下创建
   - `lib/index.js` — host 半区：导出 `name`（loader id）、`inject`（依赖服务）、`apply(ctx)`（注册命令/服务/effect）；
   - `lib/client.js` — 浏览器半区（需要 UI 时）：通过 `package.json` 的 `dsh.client` 声明挂进 `window.__DSH_BOOT__`；
   - `package.json`（`name` / `main` / `exports` / `dsh.client` 声明）、`README.md`。
2. **本地调试**：在 `%USERPROFILE%\.dsh\profiles\web\node_modules\<插件名>` 建
   **junction** 指向源工程目录，并在 `cordis.patch.yml` 末尾追加
   `- insert: - id: <id> name: '<插件名>'`，然后托盘「重启服务」生效。
   此后改代码只需改源工程一处，重启服务即可验证；日志见
   `%LOCALAPPDATA%\DSH\logs\server-*.log`。
3. **验证**：按插件功能走一遍使用路径，确认无误。
4. **放发布副本**：确认可发布后，**手动**把插件目录放入 `bundled-plugins/<插件名>/`。
   （源工程与发布副本之间**没有自动同步**，副本由维护者手动放置、手动更新。）
5. **登记并重建**：在 `plugins.go` 的 `bundledPlugins` 数组登记
   `{dir: "bundled-plugins/<插件名>", id: "<loader id>", name: "<插件名>"}`，
   在插件清单表补一行说明，然后 `.\build.ps1` 重建 EXE。新 EXE 首次启动时
   `ensureBundledPlugins` 会自动安装（幂等，`.dsh-digest` 增量）。

注意事项：

- 需要改动 `dsh-client-ui-workspace` 已发布 bundle 的插件（如往菜单加项），按
  `dsh-session-delete/patch-ui-workspace.js` 的模式写补丁脚本（锚点不匹配自动跳过）；
  升级该 bundle 后需重打补丁。
- 需要与桌面端 Go 侧通信的插件（如 `dsh-updater`），需同步修改对应的 Go 代码。
- `dsh-plug-in/` 下的 `_tmp-patch-test/` 为补丁调试用的临时目录，不是插件。

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
