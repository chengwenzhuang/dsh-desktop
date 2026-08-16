# dsh-wallpaper — 背景壁纸插件

在 **设置 → 通用** 中新增「背景壁纸」卡片：上传一张图片作为应用的背景壁纸，
可调节**不透明度**与**契合度**（铺满 / 完整显示 / 拉伸），上传后可随时**恢复默认**
（删除壁纸与设置，还原系统背景）。

## 功能

- 上传本地图片（png / jpeg / webp / gif，≤15MB）作为应用背景，立即生效。
- **不透明度**滑杆（0–100%，1% 步进，拖动即时预览，400ms 防抖持久化）：控制壁纸的可见程度。
- **契合度**下拉：`铺满（裁剪）` / `完整显示（留边）` / `拉伸铺满`。
- **恢复默认**按钮（带确认）：删除壁纸文件与设置，还原系统背景。
- 壁纸跨重启、跨页面刷新持续生效（host 半区持久化在 `<DSH_HOME>/wallpaper/`）。

## 组成

| 文件 | 作用 |
|---|---|
| `lib/index.js` | 插件 host 半区：`WallpaperService`（`get` / `set` / `update` / `clear`），图片与设置持久化在 `<DSH_HOME>/wallpaper/`（`wallpaper.<ext>` + `settings.json`） |
| `lib/client.js` | 插件浏览器半区：渲染壁纸层、注册「设置 → 通用」卡片、上传 / 调节 / 恢复默认交互 |
| `package.json` | `dsh.client` 声明，让 client-modules 把 `./client` 挂进 `window.__DSH_BOOT__` |

## 渲染原理

- 壁纸放在 `document.body` 下的全屏固定层 `#dsh-wallpaper`（`position:fixed;
  inset:0; z-index:-1; pointer-events:none`），图片经 `background-image`（data URL）、
  `background-size`（契合度）与 `opacity`（透明度）渲染。
- 同时注入 `<style id="dsh-wallpaper-css">`，把布局基色令牌
  `--dsw-alias-bg-base` 覆盖为透明（`*{--dsw-alias-bg-base:transparent !important}`）：
  应用布局底色（AppFrame 的 `background: var(--dsw-alias-bg-base)`）变为透明，
  壁纸透出；侧边栏、卡片、会话气泡等自带填充的面板不受影响，保持可读。
- 恢复默认 / 插件卸载时移除壁纸层与样式覆盖，`--dsw-alias-bg-base` 回到主题默认值。

## 本地调试 / 安装（开发机）

1. 插件源码位于 `G:\ad-dsh\dsh-desktop\dsh-plug-in\dsh-wallpaper\`；
   `C:\Users\12\.dsh\profiles\web\node_modules\dsh-wallpaper` 是指向它的目录联接
   （junction），Loader 的 `baseUrl` 即 profile 目录，可直接解析。
   改插件代码只需改源工程一处。
2. `C:\Users\12\.dsh\profiles\web\cordis.patch.yml` 已注册：

   ```yaml
   - insert:
       - id: wallpaper
         name: 'dsh-wallpaper'
   ```

3. **重启 DSH 服务**（托盘 → 重启服务，或刷新页面加载浏览器半区），
   「设置 → 通用」即出现「背景壁纸」卡片。

## 卸载

- 从 `cordis.patch.yml` 删除 `wallpaper` insert 块，删除
  `profiles\web\node_modules\dsh-wallpaper`（junction），删除
  `<DSH_HOME>\wallpaper\` 目录（壁纸数据），重启服务。
- 浏览器半区无需打补丁（不修改任何已发布 bundle），卸载即完全还原。

## 数据与限制

- 壁纸数据目录：`<DSH_HOME>/wallpaper/`（`wallpaper.<ext>` + `settings.json`，
  设置含 `mime` / `opacity` / `fit`；写盘均为原子写：先 tmp 后 rename）。
- 图片经 base64 data URL 在 RPC 中传输，host 端校验格式与 15MB 上限。
- 不透明度/契合度只影响壁纸层本身；若某处界面背景恰好也用
  `--dsw-alias-bg-base`，会随壁纸一同变透明（预期行为：壁纸透出）。
- 更换图片保留当前不透明度/契合度设置。
