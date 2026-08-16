# dsh-local-plugins — 本地插件管理

在 **设置 → 插件** 中新增「本地插件」标签页：
- **安装本地插件**：输入或浏览一个插件目录（含 package.json、与 `dsh-session-delete`
  同结构），管理器校验后复制到 `<profile>/node_modules/<name>`，并在
  `cordis.patch.yml` 追加一行 insert（带 managed 标记）。
- **移除已安装插件**：列出 cordis.patch.yml 中用户安装的插件（含加载状态），一键移除
  （删除注册行 + node_modules 副本）。
- 内置（`@deepseek-ai/*`）插件与本管理器自身受保护，不能通过它移除。

## 组成

| 文件 | 作用 |
|---|---|
| `lib/index.js` | Host 半区：`localPluginManager` 服务（list / install / remove），
  经 Typert Gateway SRC 模式暴露为 `/api/localPluginManager/*` 端点 |
| `lib/client.js` | 浏览器半区：注册 `settings.plugins.tab`（id `local`）标签页 |
| `package.json` | `dsh.client` 声明 |

## 生效机制

写入 `cordis.patch.yml` 后，profile 的 patch watcher（boot 自动挂载的
cordis-plugin-hmr）会热重载 Loader 树：**Host 半区立即生效**；含界面部分的插件
需要**刷新页面**后加载其 client bundle。若某个插件加载异常导致界面问题，重启服务
即回到上一状态（或手动从 patch 文件删掉对应行）。

## 安装（本机已完成）

1. 插件**复制**到 `C:\Users\12\.dsh\profiles\web\node_modules\dsh-local-plugins\`
   （必须复制而非 junction：它依赖 `@deepseek-ai/dsh-typert-protocol`，需要从 profile
   的模块路径解析）。
2. `cordis.patch.yml` 已注册：
   ```yaml
   - insert:
       - id: local-plugin-manager
         name: 'dsh-local-plugins'
   ```
3. 重启 DSH 服务后，设置 → 插件 会出现「本地插件」页。

> 源码放在 `G:\dsh-plug-in\dsh-local-plugins\`（统一插件目录），改动后需重新复制到
> profile 的 node_modules（本管理器不适用 junction，原因见上）。

## 迁移到新电脑

拷贝 `G:\dsh-plug-in\dsh-local-plugins` 到新机器任意位置，然后：
```powershell
# 复制到 profile（注意是复制，不是 junction）
Copy-Item -Recurse -Force "G:\dsh-plug-in\dsh-local-plugins" "$env:USERPROFILE\.dsh\profiles\web\node_modules\dsh-local-plugins"
# 在 ~/.dsh/profiles/web/cordis.patch.yml 末尾追加：
#   - insert:
#       - id: local-plugin-manager
#         name: 'dsh-local-plugins'
# 重启服务
```


## 行为说明（v0.1.0 修复）

- **安装/移除后**：立即刷新列表，并弹出确认框询问「是否现在重启 DSH 服务」——
  确认后调用 `localPluginManager/restart`（launcher 的 `appExit`，优雅关停后由
  桌面版自动拉起，约 5 秒）。
- **浏览按钮**：走原生目录选择器（`host.pickDirectory`）；不可用时提示手动输入路径。
- **SRC 参数契约**：`install(path)` 的 wire 字段就是 `path`（网关按方法源码解析参数名）。
- **移除行时**只删除目标块及其紧邻注释，保留文件头注释与其他块（已验证）。


## 行为说明（v0.1.1 修复）

- **安装前确认**：点「安装」先只读检查目录（`localPluginManager/inspect` 读取
  package.json），弹「确定要安装插件「{name}」吗？」——点取消则不会安装。
- **重启更可靠**：`restart` 用属性访问 `ctx.appExit`（沿 fiber 链向上解析 launcher
  注册的退出钩子）触发优雅关停，并带 4 秒 `process.exit(0)` 硬兜底；客户端不再等待
  响应，直接提示「正在重启 DSH…」。
- **移除后 UI 同步**：dsh-session-delete 的「删除会话」菜单项改为条件渲染
  （`window.__DSH_SESSION_DELETE__` 标志，插件加载时置位）；移除插件并刷新/重启后
  菜单项随之消失。其他插件同理（UI 都随自身 bundle 加载，移除后刷新即消失）。

## 验证

- `remoteMethods` 显示 list / install / remove 三个 Remote 端点 ✓
- install：校验 → 复制 → 追加 managed 块 → 回执 ✓
- 重复安装拒绝、自移除拒绝 ✓
- remove：删行 + 删 node_modules 副本，patch 恢复原样 ✓
- list：合并 inventory 状态（enabled / fiberPhase / present）✓
