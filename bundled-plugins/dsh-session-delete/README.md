# dsh-session-delete — 会话删除插件

在侧边栏的已有会话上点击「⋮」三个小点，菜单中新增 **删除会话** 项：
点击后会弹出确认对话框，确认后把该会话的日志移入回收站并从会话列表移除。

## 功能

- 会话行（⋮）菜单新增「删除会话」。
- 删除前弹出确认对话框（显示会话标题，含危险操作说明）。
- 删除流程（host 端 `/delete-session` 命令）：
  1. 若会话正在运行，先停止（用户取消）；
  2. 把会话日志落盘（flush）；
  3. 立即归档会话 —— 侧边栏 / 搜索 / 平铺列表马上隐藏该行；
  4. 从所属工作区的会话账目中摘除；
  5. 把日志文件移入 `<DSH_HOME>/trash/<日期>/<会话id>/`（可恢复，清空该目录即彻底删除）。
- 删除的是当前会话时自动退出该会话（回到「新会话」视图）并刷新列表。
- 重启应用后该会话完全消失（仅残留一个不可见的归档 id，无任何副作用）。

## 组成

| 文件 | 作用 |
|---|---|
| `lib/index.js` | 插件 host 半区：注册 `/delete-session` 命令（cordial plugin `session-delete`） |
| `lib/client.js` | 插件浏览器半区：监听 `dsh:session-delete` 事件，弹出确认框，调用 `commands/execute` |
| `package.json` | `dsh.client` 声明，让 client-modules 把 `./client` 挂进 `window.__DSH_BOOT__` |

另外还包含对 `dsh-client-ui-workspace` 已发布包的一处小补丁（见下文），
把菜单项接到本插件的自定义事件上。

## 已完成的安装（本机）

1. 插件源码统一放在 `G:\dsh-plug-in\dsh-session-delete\`；
   `C:\Users\12\.dsh\profiles\web\node_modules\dsh-session-delete` 是指向它的
   目录联接（junction），Loader 的 `baseUrl` 即 profile 目录，可直接解析。
   以后改插件代码只需要改 `G:\dsh-plug-in\dsh-session-delete` 一处。
2. `C:\Users\12\.dsh\profiles\web\cordis.patch.yml` 已注册：

   ```yaml
   - insert:
       - id: session-delete
         name: 'dsh-session-delete'
   ```

3. 补丁了 `dsh-client-ui-workspace/lib/client.js`（dsh 安装目录，npm cache）：
   - 菜单项数组新增 `{ id: "delete", label: t("menu.deleteSession"), icon: IconTrashOutline16 }`；
   - `onSelect` 中 `id === "delete"` 时向 `window` 派发
     `new CustomEvent("dsh:session-delete", { detail: { sessionId, title } })`；
   - zh / en 字典新增 `menu.deleteSession`。

> 注意：该补丁直接修改的是 `node_modules` 里已发布的 bundle。若以后重新安装
> 或升级 `@deepseek-ai/dsh-client-ui-workspace`，需要重新打这个补丁
> （`plugins/dsh-session-delete/patch-ui-workspace.js` 可以一键重打，见下文）。

## 让插件生效（必须重启服务）

插件与补丁都是启动时加载的，当前运行中的服务需要重启：

- 托盘图标 → 右键 → **重启服务**（或退出后重新打开 DSH.exe）。
- 重启后侧边栏会话的 ⋮ 菜单就会出现「删除会话」。

## 卸载 / 重装

**卸载**：从 `cordis.patch.yml` 删掉 `session-delete` insert 块，删除
`profiles\web\node_modules\dsh-session-delete`，并用下面的脚本还原
`dsh-client-ui-workspace` 补丁，然后重启。

**重新打补丁**（升级 ui-workspace 后）：

```powershell
node "G:\dsh-plug-in\dsh-session-delete\patch-ui-workspace.js"   # 打补丁
# node "...patch-ui-workspace.js" --revert                          # 还原
```

## 原理与限制

- 平台（rc.6）的 host 端没有 `session.delete` RPC（client 有该方法的 schema，但
  host-apiproxy 未实现），也没有公开的「销毁已附加会话」API，因此删除采用
  「归档（立即隐藏）+ 移日志到回收站（持久删除）」的组合：行立即消失，重启后彻底消失。
- 恢复：回收站位于 `~/.dsh/trash/<日期>/<会话id>/`，把目录移回
  `~/.dsh/sessions/<工作区>/` 并在 `workspace.json` 恢复账目即可（或直接忽略）。
- 该命令通过 `commands/execute` 在目标会话自己的 agent 上执行；若目标会话是
  冷会话，执行时会先被恢复（attach）一次——属平台行为，无副作用。

## 迁移到新电脑

插件是自包含的（零依赖），换机器只需两步：

1. **拷贝插件目录**：把 `dsh-session-delete` 整个文件夹拷到新机器任意位置（如 `G:\dsh-plug-in\dsh-session-delete`）。
2. **在新机器上运行安装脚本**（自动完成建 junction、注册 cordis.patch.yml、重打 ui-workspace 补丁）：

   ```powershell
   powershell -ExecutionPolicy Bypass -File G:\dsh-plug-in\dsh-session-delete\install.ps1
   ```

   或手动三步：
   ```powershell
   # 1) 建 junction（loader 从 web profile 的 node_modules 解析插件）
   New-Item -ItemType Junction -Path "$env:USERPROFILE\.dsh\profiles\web\node_modules\dsh-session-delete" -Target "G:\dsh-plug-in\dsh-session-delete"

   # 2) 在 ~/.dsh/profiles/web/cordis.patch.yml 末尾追加：
   #    - insert:
   #        - id: session-delete
   #          name: 'dsh-session-delete'

   # 3) 重打 ui-workspace 菜单补丁
   node G:\dsh-plug-in\dsh-session-delete\patch-ui-workspace.js
   ```

3. **重启 DSH 服务**（托盘 → 重启服务）。

注意事项：
- 新机器需**至少启动过一次 dsh web / DSH 桌面版**（`~/.dsh/profiles/web` 会自动生成）。
- 若新机器 DSH 版本与 rc.6 不同，`patch-ui-workspace.js` 会提示锚点不匹配——说明新版菜单结构已变（可能原生支持删除），届时可跳过补丁步骤。
- 会话数据（`~/.dsh/sessions`、`~/.dsh/storages`）与插件无关，不随插件迁移。
