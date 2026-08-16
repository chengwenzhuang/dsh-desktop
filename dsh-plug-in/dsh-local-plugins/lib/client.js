window.__ModuleLoader__.load({
	id: "dsh-local-plugins",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		let react = require("react");
		//#region dsh-local-plugins client half
		/**
		* 本地插件管理：在 设置 → 插件 中新增「本地插件」标签页。
		*
		* - 安装：输入/浏览本地插件目录 → localPluginManager/install（host 复制到
		*   profile node_modules 并写入 cordis.patch.yml，patch watcher 热重载）。
		* - 移除：列出 cordis.patch.yml 里用户安装的插件 → localPluginManager/remove。
		* - 调用走 Typert Gateway 的 SRC 端点（ctx.connection.rpc.call）。
		*/
		/** Dictionary namespace owned by this plugin. */
		const NS = "settings.localPlugins";
		/** Services required before the plugin mounts. */
		const inject = ["slots", "connection", "locale"];

		/** Simplified Chinese dictionary (the key-set source of truth). */
		const zh = {
			"tab": "本地插件",
			"loading": "正在加载…",
			"error": "加载失败",
			"retry": "重试",
			"empty": "还没有通过本地安装的插件。",
			"installTitle": "安装本地插件",
			"installHint": "选择或输入包含 package.json 的插件目录（与 dsh-session-delete 同结构）。",
			"pathPlaceholder": "例如 G:\\dsh-plug-in\\dsh-session-delete",
			"browse": "浏览…",
			"install": "安装",
			"installing": "正在安装…",
			"installedTitle": "已安装插件",
			"count": "{n} 个",
			"remove": "移除",
			"removing": "正在移除…",
			"selfTag": "本管理器",
			"notManaged": "（手动安装）",
			"confirmRemove": "确定要移除插件「{name}」吗？将删除它在 cordis.patch.yml 中的注册行和 node_modules 里的副本。",
			"enabled": "已启用",
			"disabled": "已停用",
			"phaseActive": "运行中",
			"phasePending": "待加载",
			"phaseFailed": "加载失败",
			"phaseUnloading": "卸载中",
			"phaseNull": "未加载",
			"missing": "node_modules 中不存在",
			"restartPrompt": "是否现在重启 DSH 服务，使「{what}」完全生效？重启会中断当前会话。",
			"restarting": "正在重启 DSH…",
			"browseUnavailable": "无法打开目录选择器，请手动输入路径。",
			"confirmInstall": "确定要安装插件「{name}」吗？将复制到 node_modules 并注册到 cordis.patch.yml。",
			"note": "提示：安装/移除会热重载插件树——Host 半区立即生效；含界面部分的插件需刷新页面后显示。"
		};
		/** English dictionary, checked complete against the zh key set. */
		const en = {
			"tab": "Local plugins",
			"loading": "Loading…",
			"error": "Failed to load",
			"retry": "Retry",
			"empty": "No locally installed plugins yet.",
			"installTitle": "Install a local plugin",
			"installHint": "Pick or paste a plugin directory containing package.json (same layout as dsh-session-delete).",
			"pathPlaceholder": "e.g. G:\\dsh-plug-in\\dsh-session-delete",
			"browse": "Browse…",
			"install": "Install",
			"installing": "Installing…",
			"installedTitle": "Installed plugins",
			"count": "{n}",
			"remove": "Remove",
			"removing": "Removing…",
			"selfTag": "this manager",
			"notManaged": "(manual install)",
			"confirmRemove": "Remove plugin \"{name}\"? This deletes its cordis.patch.yml row and the copy in node_modules.",
			"enabled": "Enabled",
			"disabled": "Disabled",
			"phaseActive": "Active",
			"phasePending": "Pending",
			"phaseFailed": "Failed",
			"phaseUnloading": "Unloading",
			"phaseNull": "Not loaded",
			"missing": "missing from node_modules",
			"restartPrompt": "Restart the DSH service now for \"{what}\" to fully take effect? Restarting interrupts the current session.",
			"restarting": "Restarting DSH…",
			"browseUnavailable": "Could not open a directory picker; type the path manually.",
			"confirmInstall": "Install plugin \"{name}\"? It will be copied to node_modules and registered in cordis.patch.yml.",
			"note": "Installing/removing hot-reloads the plugin tree: the host half applies immediately; UI parts appear after a page refresh."
		};

		/** 调用一个 SRC Remote 端点。 */
		async function rpcCall(ctx, endpoint, args) {
			const result = await ctx.connection.rpc.call("/api", endpoint, { args: args ?? {} });
			if (!result.ok) throw new Error(result.error?.message ?? endpoint + " failed");
			return result.value;
		}

		/** 原生目录选择器（不可用时返回 null 由调用方回退）。 */
		async function pickDirectory(ctx) {
			const api = ctx.connection.api;
			if (api === void 0 || typeof api.host?.pickDirectory !== "function") return null;
			try {
				const response = await api.host.pickDirectory({});
				const result = response?.result;
				if (result !== void 0 && result.ok) return result.value?.path ?? null;
			} catch {
				// fall through to manual input
			}
			return null;
		}

		/** 加载状态文案。 */
		function phaseLabel(phase, t) {
			switch (phase) {
				case "active": return t("phaseActive");
				case "pending": return t("phasePending");
				case "failed": return t("phaseFailed");
				case "unloading": return t("phaseUnloading");
				default: return t("phaseNull");
			}
		}

		/**
		* 本地插件管理标签页。
		* @param props - 注入面（list/install/remove/browse）+ locale t。
		* @returns 页面元素。
		*/
		function LocalPluginsTab({ list, inspect, install, remove, browse, restart, t }) {
			const [state, setState] = react.useState({ status: "loading", plugins: [] });
			const [path, setPath] = react.useState("");
			const [busy, setBusy] = react.useState(false);
			const [error, setError] = react.useState(null);
			const [notice, setNotice] = react.useState(null);

			const reload = react.useCallback(() => {
				setState({ status: "loading", plugins: [] });
				return Promise.resolve().then(list).then(
					(value) => setState({ status: "ready", plugins: value.plugins ?? [] }),
					() => setState({ status: "error", plugins: [] })
				);
			}, [list]);
			// 热重载会重建插件树：先立即刷新一次，稍后再兜底刷新一次
			const refresh = react.useCallback(() => {
				reload().catch(() => {});
				window.setTimeout(() => reload().catch(() => {}), 600);
			}, [reload]);
			react.useEffect(() => {
				reload();
			}, [reload]);

			const doBrowse = async () => {
				setError(null);
				setNotice(null);
				try {
					const picked = await browse();
					if (typeof picked === "string" && picked.length > 0) setPath(picked);
					else setNotice(t("browseUnavailable"));
				} catch (cause) {
					setError(cause instanceof Error ? cause.message : String(cause));
				}
			};
			const doInstall = async () => {
				if (busy || path.trim() === "") return;
				setError(null);
				setNotice(null);
				let info = null;
				try {
					info = await inspect(path.trim());
				} catch (cause) {
					setError(cause instanceof Error ? cause.message : String(cause));
					return;
				}
				// 安装前确认：取消则不安装
				if (!window.confirm(t("confirmInstall", { name: info.name || path.trim() }))) return;
				setBusy(true);
				try {
					const value = await install(path.trim());
					setNotice(value.note ?? "");
					setPath("");
					refresh();
					askRestart(t("tab"));
				} catch (cause) {
					setError(cause instanceof Error ? cause.message : String(cause));
				} finally {
					setBusy(false);
				}
			};
			const doRemove = async (id, name) => {
				if (busy) return;
				if (!window.confirm(t("confirmRemove", { name }))) return;
				setBusy(true);
				setError(null);
				setNotice(null);
				try {
					const value = await remove(id);
					setNotice(value.note ?? "");
					refresh();
					askRestart(name);
				} catch (cause) {
					setError(cause instanceof Error ? cause.message : String(cause));
				} finally {
					setBusy(false);
				}
			};

			const askRestart = async (what) => {
				if (typeof restart !== "function") return;
				if (!window.confirm(t("restartPrompt", { what }))) return;
				setBusy(true);
				setError(null);
				// 不等待响应：进程即将退出，直接提示正在重启
				Promise.resolve().then(() => restart()).catch(() => {});
				setNotice(t("restarting"));
			};

			const section = {
				width: "100%",
				maxWidth: "760px",
				boxSizing: "border-box",
				color: "var(--dsw-alias-label-primary)",
				display: "flex",
				flexDirection: "column",
				gap: "14px",
				fontSize: "14px",
				lineHeight: "22px"
			};
			const card = {
				border: "1px solid var(--dsw-alias-border-l2)",
				background: "var(--dsw-alias-bg-layer-3)",
				borderRadius: "10px",
				padding: "14px 16px",
				display: "flex",
				flexDirection: "column",
				gap: "10px"
			};
			const row = {
				border: "1px solid var(--dsw-alias-border-l2)",
				background: "var(--dsw-alias-bg-layer-1)",
				borderRadius: "10px",
				padding: "12px 14px",
				display: "flex",
				alignItems: "center",
				justifyContent: "space-between",
				gap: "12px"
			};
			const input = {
				border: "1px solid var(--dsw-alias-border-l2)",
				background: "var(--dsw-alias-bg-layer-1)",
				color: "var(--dsw-alias-label-primary)",
				flex: "1",
				minWidth: "0",
				height: "34px",
				borderRadius: "8px",
				outline: "none",
				padding: "0 10px",
				fontSize: "13px",
				fontFamily: "inherit"
			};
			const button = {
				height: "32px",
				padding: "0 14px",
				borderRadius: "8px",
				fontSize: "13px",
				fontWeight: "500",
				cursor: "pointer",
				fontFamily: "inherit",
				border: "1px solid var(--dsw-alias-border-l2)",
				background: "transparent",
				color: "var(--dsw-alias-label-primary)"
			};
			const danger = {
				...button,
				border: "1px solid var(--dsw-alias-state-error-primary)",
				color: "var(--dsw-alias-state-error-primary)"
			};
			const primary = {
				...button,
				border: "1px solid var(--dsw-alias-state-business-primary)",
				background: "color-mix(in srgb, var(--dsw-alias-state-business-primary) 12%, transparent)",
				color: "var(--dsw-alias-state-business-primary)"
			};
			const meta = { color: "var(--dsw-alias-label-tertiary)", fontSize: "12px", lineHeight: "18px" };
			const errorStyle = { color: "var(--dsw-alias-state-error-primary)", fontSize: "13px", lineHeight: "20px" };
			const noticeStyle = { color: "var(--dsw-alias-state-success-primary)", fontSize: "13px", lineHeight: "20px" };
			const statusDot = (ok) => ({
				display: "inline-block",
				width: "7px",
				height: "7px",
				borderRadius: "999px",
				flex: "none",
				background: ok ? "var(--dsw-alias-state-success-primary)" : "var(--dsw-alias-state-error-primary)"
			});

			return react.createElement("div", { style: section },
				react.createElement("h3", { style: { margin: "0", fontSize: "14px", fontWeight: "600", lineHeight: "20px" } }, t("installTitle")),
				react.createElement("div", { style: card },
					react.createElement("p", { style: { ...meta, margin: "0" } }, t("installHint")),
					react.createElement("div", { style: { display: "flex", gap: "8px", alignItems: "center" } },
						react.createElement("input", {
							type: "text",
							value: path,
							placeholder: t("pathPlaceholder"),
							"aria-label": t("installTitle"),
							style: input,
							onChange: (event) => setPath(event.currentTarget.value),
							onKeyDown: (event) => {
								if (event.key === "Enter") doInstall();
							}
						}),
						react.createElement("button", { type: "button", style: button, onClick: doBrowse, disabled: busy }, t("browse")),
						react.createElement("button", { type: "button", style: primary, onClick: doInstall, disabled: busy || path.trim() === "" }, busy ? t("installing") : t("install"))
					),
					error !== null ? react.createElement("p", { role: "alert", style: errorStyle }, error) : null,
					notice !== null ? react.createElement("p", { role: "status", style: noticeStyle }, notice) : null
				),
				react.createElement("p", { style: { ...meta, margin: "0" } }, t("note")),
				react.createElement("h3", { style: { margin: "12px 0 0", fontSize: "14px", fontWeight: "600", lineHeight: "20px" } },
					t("installedTitle"),
					state.status === "ready" ? " (" + t("count", { n: state.plugins.length }) + ")" : ""
				),
				state.status === "loading" ? react.createElement("p", { style: meta }, t("loading")) : null,
				state.status === "error" ? react.createElement("div", { style: { display: "flex", alignItems: "center", gap: "10px" } },
					react.createElement("p", { role: "alert", style: errorStyle }, t("error")),
					react.createElement("button", { type: "button", style: button, onClick: reload }, t("retry"))
				) : null,
				state.status === "ready" && state.plugins.length === 0 ? react.createElement("p", { style: meta }, t("empty")) : null,
				state.status === "ready" ? react.createElement("div", { style: { display: "flex", flexDirection: "column", gap: "8px" } },
					state.plugins.map((plugin) => react.createElement("div", { key: plugin.id, style: row, "data-plugin-name": plugin.name },
						react.createElement("div", { style: { display: "flex", flexDirection: "column", gap: "2px", minWidth: "0" } },
							react.createElement("span", { style: { fontSize: "14px", fontWeight: "600", lineHeight: "20px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" } },
								plugin.name,
								plugin.self ? " " + t("selfTag") : "",
								plugin.managed ? "" : " " + t("notManaged")
							),
							react.createElement("span", { style: meta, overflowWrap: "anywhere" },
								plugin.id,
								plugin.enabled === null ? "" : " · " + (plugin.enabled ? t("enabled") : t("disabled")),
								" · " + phaseLabel(plugin.fiberPhase, t),
								plugin.present ? "" : " · " + t("missing")
							)
						),
						react.createElement("div", { style: { display: "flex", alignItems: "center", gap: "10px", flex: "none" } },
							react.createElement("span", { style: statusDot(plugin.enabled !== false && plugin.fiberPhase !== "failed"), "aria-hidden": "true" }),
							plugin.self ? null : react.createElement("button", { type: "button", style: danger, onClick: () => doRemove(plugin.id, plugin.name), disabled: busy }, busy ? t("removing") : t("remove"))
						)
					))
				) : null
			);
		}

		/**
		* 注册设置 → 插件 的新标签页。
		* @param ctx - 浏览器根上下文（slots + connection + locale）。
		*/
		function apply(ctx) {
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-local-plugins: dictionaries");
			const t = ctx.locale.bind(NS);
			const injected = () => ({
				list: () => rpcCall(ctx, "localPluginManager/list"),
				inspect: (sourcePath) => rpcCall(ctx, "localPluginManager/inspect", { path: sourcePath }),
				install: (sourcePath) => rpcCall(ctx, "localPluginManager/install", { path: sourcePath }),
				remove: (id) => rpcCall(ctx, "localPluginManager/remove", { id }),
				browse: () => pickDirectory(ctx),
				restart: () => rpcCall(ctx, "localPluginManager/restart")
			});
			ctx.slots.inject("settings.plugins.tab", () => ctx.slots.register({
				name: "settings.plugins.tab",
				id: "local",
				order: 20,
				label: () => t("tab"),
				locale: NS,
				inject: injected
			}, LocalPluginsTab));
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map
