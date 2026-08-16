window.__ModuleLoader__.load({
	id: "dsh-updater",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		let react = require("react");
		//#region dsh-updater client half
		/**
		* 软件更新：设置 → 通用 中的「软件更新」卡片（当前版本 / 最新版本 / 检测更新 /
		* 立即更新），以及每次打开 DSH 后若检测到新版本自动弹出的询问对话框（含
		* 「不再提示」勾选，默认不勾选，勾选后本版本不再自动弹窗）。
		*
		* 数据来自桌面端 updater：updater/status 读状态，updater/check 与
		* updater/apply 向 request.json 写请求，桌面端轮询消费。
		*/
		/** Dictionary namespace owned by this plugin. */
		const NS = "settings.updater";
		/** Services required before the plugin mounts. */
		const inject = ["slots", "connection", "locale"];

		const zh = {
			"cardTitle": "软件更新",
			"cardDescription": "从 GitHub 检测并安装新版本（需要桌面端 DSH.exe 配合）。",
			"current": "当前版本",
			"latest": "最新版本",
			"status.checking": "正在检测…",
			"status.available": "发现新版本，可以更新",
			"status.upToDate": "已是最新版本",
			"status.unconfigured": "更新源未配置",
			"status.downloading": "正在下载更新…",
			"status.ready": "更新已就绪",
			"status.error": "检测失败",
			"status.noUpdater": "未检测到桌面端",
			"check": "检测更新",
			"checking": "检测中…",
			"update": "立即更新",
			"updating": "正在更新，应用即将重启…",
			"dialogTitle": "发现新版本",
			"dialogBody": "发现新版本 v{latest}（当前 v{current}）。是否立即更新？更新完成后将自动重启 DSH。",
			"releaseNotes": "更新内容",
			"noPrompt": "本版本不再提示",
			"cancel": "取消",
			"close": "关闭",
			"retry": "重试",
			"loading": "正在读取更新状态…",
			"error": "读取更新状态失败",
		};
		const en = {
			"cardTitle": "Software update",
			"cardDescription": "Check and install new versions from GitHub (requires the DSH.exe desktop app).",
			"current": "Current version",
			"latest": "Latest version",
			"status.checking": "Checking…",
			"status.available": "A new version is available",
			"status.upToDate": "Up to date",
			"status.unconfigured": "Update source not configured",
			"status.downloading": "Downloading update…",
			"status.ready": "Update ready",
			"status.error": "Check failed",
			"status.noUpdater": "Desktop updater not detected",
			"check": "Check for updates",
			"checking": "Checking…",
			"update": "Update now",
			"updating": "Updating — the app will restart…",
			"dialogTitle": "New version available",
			"dialogBody": "Version v{latest} is available (current v{current}). Update now? DSH will restart automatically afterwards.",
			"releaseNotes": "Release notes",
			"noPrompt": "Don't ask again for this version",
			"cancel": "Cancel",
			"close": "Close",
			"retry": "Retry",
			"loading": "Reading update status…",
			"error": "Failed to read update status",
		};

		async function rpcCall(ctx, endpoint, args) {
			const result = await ctx.connection.rpc.call("/api", endpoint, { args: args ?? {} });
			if (!result.ok) throw new Error(result.error?.message ?? endpoint + " failed");
			return result.value;
		}

		function statusLabel(status, t) {
			switch (status) {
				case "checking": return t("status.checking");
				case "available": return t("status.available");
				case "up-to-date": return t("status.upToDate");
				case "unconfigured": return t("status.unconfigured");
				case "downloading": return t("status.downloading");
				case "ready": return t("status.ready");
				case "error": return t("status.error");
				default: return status;
			}
		}

		// ── 全局自动弹窗（每次打开 DSH 检测到新版本时） ──
		const DISMISS_KEY = "dsh-updater-dismissed";
		let dialog = null;
		// 弹窗内进度轮询所需的 RPC 上下文（apply() 时赋值；卡片路径弹窗 ctx 为 null）
		let serviceCtx = null;
		function closeDialog() {
			if (dialog !== null) {
				if (dialog._onKey) window.removeEventListener("keydown", dialog._onKey);
				if (dialog._progressTimer) { window.clearInterval(dialog._progressTimer); dialog._progressTimer = null; }
				dialog.remove();
			}
			dialog = null;
		}
		function isDismissed(latestVersion) {
			try { return window.localStorage.getItem(DISMISS_KEY) === latestVersion; } catch { return false; }
		}
		function dismiss(latestVersion) {
			try { window.localStorage.setItem(DISMISS_KEY, latestVersion); } catch {}
		}

		// 更新进行中：在弹窗卡片下方渲染进度条 + 百分比，并轮询 updater/status
		// 刷新进度；ready / error 时停止轮询。定时器挂在 overlay 上，弹窗关闭时
		// closeDialog 负责清理。
		function startUpdateProgress(card, overlay, body, t) {
			if (overlay._progressTimer) return;
			const wrap = document.createElement("div");
			wrap.style.cssText = "display:flex;flex-direction:column;gap:6px";
			const text = document.createElement("div");
			text.style.cssText = "font-size:12px;line-height:18px;color:var(--dsw-alias-label-secondary, #666)";
			text.textContent = "0%";
			const track = document.createElement("div");
			track.style.cssText = "height:6px;border-radius:3px;background:var(--dsw-alias-bg-layer-1, #ececec);overflow:hidden";
			const bar = document.createElement("div");
			bar.style.cssText = "height:100%;width:0%;border-radius:3px;background:var(--dsw-alias-state-business-primary, #2563eb);transition:width .3s ease";
			track.appendChild(bar);
			wrap.append(text, track);
			card.appendChild(wrap);
			const setProgress = (pct, done) => {
				const p = Math.max(0, Math.min(100, Math.round(pct || 0)));
				bar.style.width = p + "%";
				text.textContent = p + "%";
				if (done && overlay._progressTimer) {
					window.clearInterval(overlay._progressTimer);
					overlay._progressTimer = null;
				}
			};
			overlay._progressTimer = window.setInterval(() => {
				Promise.resolve().then(() => rpcCall(serviceCtx, "updater/status")).then(
					(s) => {
						if (!s) return;
						if (s.status === "downloading") {
							setProgress(s.progress, false);
						} else if (s.status === "ready") {
							body.textContent = t("status.ready");
							setProgress(100, true);
						} else if (s.status === "error") {
							body.textContent = t("status.error") + "：" + (s.error || "");
							bar.style.background = "var(--dsw-alias-state-error-primary, #dc2626)";
							setProgress(s.progress, true);
						} else {
							// apply 请求尚未被桌面端消费（仍 available/idle），维持当前进度
							setProgress(s.progress, false);
						}
					},
					() => { /* 瞬时失败下一轮再试 */ }
				);
			}, 1000);
		}

		function showUpdateDialog(ctx, state, t, onUpdate) {
			if (dialog !== null) closeDialog();
			if (!state.latestVersion || isDismissed(state.latestVersion)) return;
			const overlay = document.createElement("div");
			overlay.style.cssText = "position:fixed;inset:0;z-index:2147483000;display:flex;align-items:center;justify-content:center;padding:24px;box-sizing:border-box;font-family:var(--dsh-font-family, system-ui, -apple-system, 'Segoe UI', sans-serif)";
			const mask = document.createElement("div");
			mask.style.cssText = "position:absolute;inset:0;background:var(--dsw-alias-bg-mask-1, rgba(0,0,0,.24));-webkit-backdrop-filter:var(--dsw-mask-blur, blur(4px));backdrop-filter:var(--dsw-mask-blur, blur(4px))";
			overlay.appendChild(mask);
			const card = document.createElement("div");
			card.setAttribute("role", "dialog");
			card.setAttribute("aria-modal", "true");
			card.style.cssText = "position:relative;z-index:1;display:flex;flex-direction:column;gap:14px;width:min(440px,100%);box-sizing:border-box;padding:22px 24px 24px;border:1px solid var(--dsw-alias-border-inverted, rgba(0,0,0,.1));border-radius:24px;background:var(--dsw-alias-bg-layer-2, #fff);box-shadow:var(--dsw-shadow-lv3, 0 8px 30px rgba(0,0,0,.2));color:var(--dsw-alias-label-primary, #222)";
			const title = document.createElement("div");
			title.style.cssText = "margin:0;font-size:16px;line-height:24px;font-weight:600;color:var(--dsw-alias-label-primary, #222)";
			title.textContent = t("dialogTitle");
			const relTitle = document.createElement("div");
			relTitle.style.cssText = "margin:0;font-size:15px;line-height:22px;font-weight:600;color:var(--dsw-alias-state-business-primary, #2563eb)";
			relTitle.textContent = state.latestTitle || "";
			const body = document.createElement("div");
			body.style.cssText = "margin:0;font-size:14px;line-height:22px;color:var(--dsw-alias-label-primary, #333)";
			body.textContent = t("dialogBody", { latest: state.latestVersion, current: state.currentVersion || "-" });
			let notesLabel = null;
			let notesBox = null;
			if (state.latestNotes) {
				notesLabel = document.createElement("div");
				notesLabel.style.cssText = "margin:0;font-size:12px;line-height:18px;font-weight:600;color:var(--dsw-alias-label-secondary, #666)";
				notesLabel.textContent = t("releaseNotes");
				notesBox = document.createElement("div");
				notesBox.style.cssText = "margin:0;max-height:170px;overflow-y:auto;padding:10px 12px;border:1px solid var(--dsw-alias-border-l2, rgba(0,0,0,.12));border-radius:8px;background:var(--dsw-alias-bg-layer-1, #fafafa);font-size:12px;line-height:20px;color:var(--dsw-alias-label-secondary, #555);white-space:pre-wrap;word-break:break-word;box-sizing:border-box";
				notesBox.textContent = state.latestNotes;
			}
			const noPrompt = document.createElement("label");
			noPrompt.style.cssText = "display:flex;align-items:center;gap:8px;font-size:13px;line-height:20px;color:var(--dsw-alias-label-secondary, #666);cursor:pointer";
			const checkbox = document.createElement("input");
			checkbox.type = "checkbox";
			checkbox.style.cssText = "margin:0";
			noPrompt.appendChild(checkbox);
			noPrompt.appendChild(document.createTextNode(t("noPrompt")));
			const footer = document.createElement("div");
			footer.style.cssText = "display:flex;align-items:center;justify-content:flex-end;gap:8px;margin-top:2px";
			const mkBtn = (label, danger) => {
				const b = document.createElement("button");
				b.type = "button";
				b.textContent = label;
				b.style.cssText = "height:32px;padding:0 14px;border-radius:8px;font-size:13px;font-weight:500;cursor:pointer;font-family:inherit;" + (danger ? "background:var(--dsw-alias-state-business-primary, #2563eb);border:1px solid transparent;color:#fff" : "background:transparent;border:1px solid var(--dsw-alias-border-l2, rgba(0,0,0,.2));color:var(--dsw-alias-label-primary, #222)");
				return b;
			};
			const cancelBtn = mkBtn(t("cancel"), false);
			cancelBtn.addEventListener("click", () => {
				if (checkbox.checked && state.latestVersion) dismiss(state.latestVersion);
				closeDialog();
			});
			const updateBtn = mkBtn(t("update"), true);
			updateBtn.addEventListener("click", () => {
				if (checkbox.checked && state.latestVersion) dismiss(state.latestVersion);
				// 进入更新流程：隐藏「本版本不再提示」与两个操作按钮，下方显示下载进度
				noPrompt.style.display = "none";
				body.textContent = t("status.downloading");
				card.removeChild(footer);
				startUpdateProgress(card, overlay, body, t);
				onUpdate();
			});
			footer.append(cancelBtn, updateBtn);
			const parts = [title];
			if (state.latestTitle) parts.push(relTitle);
			parts.push(body);
			if (notesLabel && notesBox) parts.push(notesLabel, notesBox);
			parts.push(noPrompt, footer);
			card.append(...parts);
			overlay.appendChild(card);
			document.body.appendChild(overlay);
			dialog = overlay;
			const onKey = (e) => { if (e.key === "Escape") { if (checkbox.checked && state.latestVersion) dismiss(state.latestVersion); closeDialog(); } };
			window.addEventListener("keydown", onKey);
			dialog._onKey = onKey;
		}

		/**
		* 设置 → 通用 的「软件更新」卡片。
		*/
		function UpdaterCard({ status, check, update, t }) {
			const [state, setState] = react.useState({ loading: true, status: "idle", currentVersion: "", latestVersion: null, error: null });
			const [busy, setBusy] = react.useState(false);
			const load = react.useCallback(() => {
				setState((prev) => ({ ...prev, loading: true }));
				Promise.resolve().then(status).then(
					(value) => setState({ loading: false, ...value, error: value.error ?? null }),
					(cause) => setState({ loading: false, status: "error", currentVersion: "", latestVersion: null, error: cause instanceof Error ? cause.message : String(cause) })
				);
			}, [status]);
			react.useEffect(() => { load(); const timer = window.setInterval(load, 5000); return () => window.clearInterval(timer); }, [load]);
			const doCheck = async () => { setBusy(true); try { await check(); window.setTimeout(load, 1500); } catch (e) { setState((p) => ({ ...p, error: e instanceof Error ? e.message : String(e) })); } finally { setBusy(false); } };
			const doUpdate = async () => { setBusy(true); try { await update(); } catch (e) { setState((p) => ({ ...p, error: e instanceof Error ? e.message : String(e) })); setBusy(false); } };
			// 点「立即更新」先弹出与新版本弹窗一致的确认对话框（含 Release 标题/说明）
			const onUpdateClick = () => showUpdateDialog(null, state, t, doUpdate);
			const card = { border: "1px solid var(--dsw-alias-border-l2)", background: "var(--dsw-alias-bg-layer-3)", borderRadius: "10px", padding: "14px 16px", display: "flex", flexDirection: "column", gap: "10px", maxWidth: "760px" };
			const row = { display: "flex", alignItems: "center", justifyContent: "space-between", gap: "12px" };
			const meta = { color: "var(--dsw-alias-label-tertiary)", fontSize: "12px", lineHeight: "18px" };
			const btn = { height: "32px", padding: "0 14px", borderRadius: "8px", fontSize: "13px", fontWeight: "500", cursor: "pointer", fontFamily: "inherit", border: "1px solid var(--dsw-alias-border-l2)", background: "transparent", color: "var(--dsw-alias-label-primary)" };
			const primary = { ...btn, border: "1px solid var(--dsw-alias-state-business-primary)", background: "color-mix(in srgb, var(--dsw-alias-state-business-primary) 12%, transparent)", color: "var(--dsw-alias-state-business-primary)" };
			const errorStyle = { color: "var(--dsw-alias-state-error-primary)", fontSize: "13px", lineHeight: "20px" };
			const statusLine = state.loading ? t("loading") : state.status === "unconfigured" && !state.present ? t("status.noUpdater") : statusLabel(state.status, t) + (state.status === "downloading" && typeof state.progress === "number" ? " " + Math.round(state.progress) + "%" : "") + (state.error ? "：" + state.error : "");
			return react.createElement("div", { style: card },
				react.createElement("div", { style: { fontSize: "14px", fontWeight: "600", lineHeight: "20px", color: "var(--dsw-alias-label-primary)" } }, t("cardTitle")),
				react.createElement("div", { style: { ...meta, margin: "0" } }, t("cardDescription")),
				react.createElement("div", { style: row },
					react.createElement("div", { style: { display: "flex", flexDirection: "column", gap: "2px" } },
						react.createElement("span", { style: { fontSize: "13px", lineHeight: "20px", color: "var(--dsw-alias-label-primary)" } }, t("current") + "：" + (state.currentVersion || "—")),
						react.createElement("span", { style: { fontSize: "13px", lineHeight: "20px", color: "var(--dsw-alias-label-primary)" } }, t("latest") + "：" + (state.latestVersion || "—"))
				),
					react.createElement("div", { style: { display: "flex", gap: "8px", flex: "none" } },
						react.createElement("button", { type: "button", style: btn, onClick: doCheck, disabled: busy }, busy ? t("checking") : t("check")),
						state.status === "available" ? react.createElement("button", { type: "button", style: primary, onClick: onUpdateClick, disabled: busy }, t("update")) : null
				)
			),
			state.status === "error" ? react.createElement("p", { role: "alert", style: errorStyle }, statusLine) : react.createElement("p", { style: { ...meta, margin: "0" } }, statusLine)
			);
		}

		/**
		* 注册设置卡片 + 启动自动检测弹窗。
		*/
		function apply(ctx) {
			ctx.effect(() => ctx.locale.register(NS, { zh, en }), "ui-updater: dictionaries");
			const t = ctx.locale.bind(NS);
			serviceCtx = ctx;
			const injected = () => ({
				status: () => rpcCall(ctx, "updater/status"),
				check: () => rpcCall(ctx, "updater/check"),
				update: () => rpcCall(ctx, "updater/apply"),
			});
			ctx.slots.inject("settings.general.item", () => ctx.slots.register({
				name: "settings.general.item",
				id: "updater",
				order: 20,
				locale: NS,
				inject: injected
			}, UpdaterCard));
			// 启动自动检测：读取状态，若发现新版本且未被「不再提示」跳过 → 弹窗
			let asked = false;
			const pollStatus = () => {
				Promise.resolve().then(() => rpcCall(ctx, "updater/status")).then((state) => {
					if (asked) return;
					if (state && state.status === "available") {
						asked = true;
						showUpdateDialog(ctx, state, t, () => {
							// 点更新：请求桌面端下载并重启
							Promise.resolve().then(() => rpcCall(ctx, "updater/apply")).catch(() => {});
						});
					}
				}).catch(() => {});
				};
				window.setTimeout(pollStatus, 1500);
				window.setInterval(pollStatus, 10000);
				return () => { closeDialog(); };
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map
