window.__ModuleLoader__.load({
	id: "dsh-wallpaper",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		let react = require("react");
		//#region dsh-wallpaper client half
		/**
		* 背景壁纸：设置 → 通用 中的「背景壁纸」卡片——上传图片、调节不透明度与
		* 契合度（铺满 / 完整显示 / 拉伸），或恢复默认背景。
		*
		* 渲染机制：壁纸放在全屏固定层（z-index:-1），同时把应用的基色令牌
		* --dsw-alias-bg-base 覆盖为透明，让壁纸从布局底色下透出；侧边栏、
		* 卡片、会话气泡等自带填充的面板不受影响，保持可读。
		*
		* 数据经 host 半区持久化在 <DSH_HOME>/wallpaper/（wallpaper.<ext> +
		* settings.json），端点 wallpaper/get | set | update | clear。
		*/
		/** Dictionary namespace owned by this plugin. */
		const NS = "settings.wallpaper";
		/** Services required before the plugin mounts. */
		const inject = ["slots", "connection", "locale"];

		/** Simplified Chinese dictionary (the key-set source of truth). */
		const zh = {
			"cardTitle": "背景壁纸",
			"cardDescription": "上传一张图片作为应用背景，可调节不透明度与契合度；恢复默认即还原系统背景。",
			"notSet": "尚未设置壁纸",
			"upload": "上传图片",
			"uploading": "上传中…",
			"uploaded": "壁纸已应用",
			"opacity": "不透明度",
			"fit": "契合度",
			"fit.cover": "铺满（裁剪）",
			"fit.contain": "完整显示（留边）",
			"fit.stretch": "拉伸铺满",
			"restoreDefault": "恢复默认",
			"confirmClear": "确定恢复默认背景吗？当前壁纸与设置将被删除。",
			"cleared": "已恢复默认背景",
			"loading": "正在读取…",
			"badType": "请选择图片文件（png / jpeg / webp / gif）",
			"readFailed": "读取图片失败",
			"sizeHint": "建议不超过 15MB"
		};
		/** English dictionary, checked complete against the zh key set. */
		const en = {
			"cardTitle": "Background wallpaper",
			"cardDescription": "Upload an image as the app background, tune opacity and fit; restore the default to reset.",
			"notSet": "No wallpaper set",
			"upload": "Upload image",
			"uploading": "Uploading…",
			"uploaded": "Wallpaper applied",
			"opacity": "Opacity",
			"fit": "Fit",
			"fit.cover": "Cover (crop)",
			"fit.contain": "Contain (letterbox)",
			"fit.stretch": "Stretch",
			"restoreDefault": "Restore default",
			"confirmClear": "Restore the default background? The current wallpaper and its settings will be deleted.",
			"cleared": "Default background restored",
			"loading": "Loading…",
			"badType": "Please pick an image file (png / jpeg / webp / gif)",
			"readFailed": "Failed to read the image",
			"sizeHint": "Keep it under 15 MB"
		};

		/** 调用一个 SRC Remote 端点。 */
		async function rpcCall(ctx, endpoint, args) {
			const result = await ctx.connection.rpc.call("/api", endpoint, { args: args ?? {} });
			if (!result.ok) throw new Error(result.error?.message ?? endpoint + " failed");
			return result.value;
		}

		// ── 壁纸渲染（由插件生命周期持有，设置页导航离开不卸载） ──
		/** 契合度 → background-size。 */
		const FIT_SIZE = { cover: "cover", contain: "contain", stretch: "100% 100%" };
		let layer = null;
		let styleTag = null;
		let uiStyleTag = null;

		/** 注入基色透明覆盖样式（幂等）。 */
		function ensureWallpaperStyle() {
			if (styleTag !== null) return;
			styleTag = document.createElement("style");
			styleTag.id = "dsh-wallpaper-css";
			styleTag.textContent = [
				"*{--dsw-alias-bg-base:transparent !important}",
				"html,body{background:transparent !important}"
			].join("\n");
			document.head.appendChild(styleTag);
		}

		/** 注入设置卡片 UI 样式（自定义滑杆轨道/滑块；幂等）。 */
		function ensureUiStyle() {
			if (uiStyleTag !== null) return;
			uiStyleTag = document.createElement("style");
			uiStyleTag.id = "dsh-wallpaper-ui-css";
			uiStyleTag.textContent = [
				".wallpaper-range{-webkit-appearance:none;appearance:none;height:6px;border-radius:3px;outline:none;cursor:pointer}",
				".wallpaper-range::-webkit-slider-thumb{-webkit-appearance:none;appearance:none;width:16px;height:16px;border-radius:50%;background:var(--dsw-alias-state-business-primary);border:2px solid var(--dsw-alias-bg-layer-1);box-shadow:0 1px 3px rgba(0,0,0,.25);cursor:pointer}",
				".wallpaper-range::-moz-range-thumb{width:16px;height:16px;border-radius:50%;background:var(--dsw-alias-state-business-primary);border:2px solid var(--dsw-alias-bg-layer-1);box-shadow:0 1px 3px rgba(0,0,0,.25);cursor:pointer}",
				".wallpaper-range::-moz-range-track{height:6px;border-radius:3px;background:transparent}"
			].join("\n");
			document.head.appendChild(uiStyleTag);
		}

		/**
		* 应用 / 更新壁纸层。
		* @param wallpaper - host 返回的壁纸状态（present / image / opacity / fit）。
		*/
		function applyWallpaper(wallpaper) {
			if (wallpaper === null || wallpaper.present !== true || typeof wallpaper.image !== "string") {
				removeWallpaper();
				return;
			}
			ensureWallpaperStyle();
			if (layer === null) {
				layer = document.createElement("div");
				layer.id = "dsh-wallpaper";
				layer.setAttribute("aria-hidden", "true");
				layer.style.cssText = "position:fixed;inset:0;z-index:-1;pointer-events:none;background-position:center;background-repeat:no-repeat;background-attachment:fixed";
				document.body.appendChild(layer);
			}
			layer.style.backgroundImage = "url(\"" + wallpaper.image + "\")";
			layer.style.backgroundSize = FIT_SIZE[wallpaper.fit] ?? "cover";
			layer.style.opacity = String(typeof wallpaper.opacity === "number" ? wallpaper.opacity : 0.6);
		}

		/** 移除壁纸层与样式覆盖（恢复默认背景）。 */
		function removeWallpaper() {
			if (layer !== null) {
				layer.remove();
				layer = null;
			}
			if (styleTag !== null) {
				styleTag.remove();
				styleTag = null;
			}
		}

		/**
		* 设置 → 通用 的「背景壁纸」卡片。
		* @param props - 注入面（get/set/update/clear）+ locale t。
		* @returns 卡片元素。
		*/
		function WallpaperCard({ get, set, update, clear, t }) {
			const [state, setState] = react.useState({ loading: true, present: false, image: null, opacity: 0.6, fit: "cover", error: null, notice: null });
			const [busy, setBusy] = react.useState(false);
			const fileRef = react.useRef(null);
			const persistTimer = react.useRef(null);
			const rangeRef = react.useRef(null);

			const load = react.useCallback(() => {
				setState((prev) => ({ ...prev, loading: true }));
				return Promise.resolve().then(get).then((value) => {
					setState({
						loading: false,
						present: value.present === true,
						image: value.present === true ? value.image : null,
						opacity: typeof value.opacity === "number" ? value.opacity : 0.6,
						fit: value.fit ?? "cover",
						error: null,
						notice: null
					});
					applyWallpaper(value);
				}).catch((cause) => setState((prev) => ({
					...prev,
					loading: false,
					error: cause instanceof Error ? cause.message : String(cause)
				})));
			}, [get]);

			react.useEffect(() => {
				load();
				return () => {
					if (persistTimer.current !== null) window.clearTimeout(persistTimer.current);
				};
			}, [load]);

			// 滑杆轨道填充：值变化时重绘「已填充」渐变（自定义 track/thumb 见 ui 样式表）
			react.useEffect(() => {
				const el = rangeRef.current;
				if (el === null) return;
				const percent = Math.round((typeof state.opacity === "number" ? state.opacity : 0.6) * 100);
				el.style.background = "linear-gradient(to right, var(--dsw-alias-state-business-primary) 0%, var(--dsw-alias-state-business-primary) " + percent + "%, var(--dsw-alias-border-l2) " + percent + "%, var(--dsw-alias-border-l2) 100%)";
			}, [state.opacity]);

			/** 不透明度 / 契合度变化：本地即时生效 + 防抖持久化。 */
			const schedulePersist = (next) => {
				setState((prev) => ({ ...prev, opacity: next.opacity, fit: next.fit }));
				applyWallpaper({ present: true, image: state.image, opacity: next.opacity, fit: next.fit });
				if (persistTimer.current !== null) window.clearTimeout(persistTimer.current);
				persistTimer.current = window.setTimeout(() => {
					Promise.resolve().then(() => update({ opacity: next.opacity, fit: next.fit })).then(applyWallpaper).catch(() => {});
				}, 400);
			};

			const doPick = () => {
				if (fileRef.current !== null) fileRef.current.click();
			};

			const doFile = (event) => {
				const file = event.currentTarget.files?.[0];
				event.currentTarget.value = "";
				if (file === void 0) return;
				if (typeof file.type !== "string" || !file.type.startsWith("image/")) {
					setState((prev) => ({ ...prev, error: t("badType"), notice: null }));
					return;
				}
				const reader = new FileReader();
				reader.onload = () => {
					const dataUrl = String(reader.result ?? "");
					setBusy(true);
					Promise.resolve().then(() => set({ image: dataUrl, name: file.name })).then((value) => {
						setState((prev) => ({
							...prev,
							present: value.present === true,
							image: value.present === true ? value.image : null,
							opacity: typeof value.opacity === "number" ? value.opacity : prev.opacity,
							fit: value.fit ?? prev.fit,
							error: null,
							notice: t("uploaded")
						}));
						applyWallpaper(value);
					}).catch((cause) => setState((prev) => ({
						...prev,
						error: cause instanceof Error ? cause.message : String(cause),
						notice: null
					}))).finally(() => setBusy(false));
				};
				reader.onerror = () => setState((prev) => ({ ...prev, error: t("readFailed"), notice: null }));
				reader.readAsDataURL(file);
			};

			const doOpacity = (event) => {
				schedulePersist({ ...state, opacity: Number(event.currentTarget.value) });
			};

			const doFit = (event) => {
				schedulePersist({ ...state, fit: event.currentTarget.value });
			};

			const doClear = () => {
				if (!window.confirm(t("confirmClear"))) return;
				setBusy(true);
				Promise.resolve().then(clear).then((value) => {
					setState((prev) => ({
						...prev,
						present: false,
						image: null,
						opacity: 0.6,
						fit: "cover",
						error: null,
						notice: t("cleared")
					}));
					removeWallpaper();
					void value;
				}).catch((cause) => setState((prev) => ({
					...prev,
					error: cause instanceof Error ? cause.message : String(cause),
					notice: null
				}))).finally(() => setBusy(false));
			};

			// ── 样式常量（与 dsh-updater / dsh-local-plugins 一致的令牌用法） ──
			const card = {
				marginTop: "14px",
				border: "1px solid var(--dsw-alias-border-l2)",
				background: "var(--dsw-alias-bg-layer-3)",
				borderRadius: "10px",
				padding: "14px 16px",
				display: "flex",
				flexDirection: "column",
				gap: "12px",
				maxWidth: "760px"
			};
			const row = {
				display: "flex",
				alignItems: "center",
				justifyContent: "space-between",
				gap: "12px"
			};
			const meta = { color: "var(--dsw-alias-label-tertiary)", fontSize: "12px", lineHeight: "18px" };
			const label = { fontSize: "13px", lineHeight: "20px", color: "var(--dsw-alias-label-primary)" };
			const btn = {
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
			const primary = {
				...btn,
				border: "1px solid var(--dsw-alias-state-business-primary)",
				background: "color-mix(in srgb, var(--dsw-alias-state-business-primary) 12%, transparent)",
				color: "var(--dsw-alias-state-business-primary)"
			};
			const danger = {
				...btn,
				border: "1px solid var(--dsw-alias-state-error-primary)",
				color: "var(--dsw-alias-state-error-primary)"
			};
			const range = {
				flex: "1",
				minWidth: "0",
				height: "6px",
				borderRadius: "3px",
				cursor: "pointer",
				background: "var(--dsw-alias-border-l2)"
			};
			const select = {
				border: "1px solid var(--dsw-alias-border-l2)",
				background: "var(--dsw-alias-bg-layer-1)",
				color: "var(--dsw-alias-label-primary)",
				height: "32px",
				borderRadius: "8px",
				padding: "0 8px",
				fontSize: "13px",
				fontFamily: "inherit",
				cursor: "pointer"
			};
			const preview = {
				maxWidth: "100%",
				maxHeight: "140px",
				borderRadius: "8px",
				border: "1px solid var(--dsw-alias-border-l2)",
				objectFit: "cover"
			};
			const errorStyle = { color: "var(--dsw-alias-state-error-primary)", fontSize: "13px", lineHeight: "20px" };
			const noticeStyle = { color: "var(--dsw-alias-state-success-primary)", fontSize: "13px", lineHeight: "20px" };

			const opacityPercent = Math.round((typeof state.opacity === "number" ? state.opacity : 0.6) * 100);

			return react.createElement("div", { style: card },
				react.createElement("div", { style: { fontSize: "14px", fontWeight: "600", lineHeight: "20px", color: "var(--dsw-alias-label-primary)" } }, t("cardTitle")),
				react.createElement("div", { style: { ...meta, margin: "0" } }, t("cardDescription")),

				state.loading
					? react.createElement("p", { style: meta }, t("loading"))
					: react.createElement(react.Fragment, null,
						// 预览
						state.present && state.image !== null
							? react.createElement("img", { src: state.image, alt: t("cardTitle"), style: preview })
							: react.createElement("p", { style: { ...meta, margin: "0" } }, t("notSet")),

						// 上传
						react.createElement("div", { style: row },
							react.createElement("span", { style: label }, t("sizeHint")),
							react.createElement("div", { style: { display: "flex", gap: "8px", flex: "none", alignItems: "center" } },
								react.createElement("input", {
									ref: fileRef,
									type: "file",
									accept: "image/png,image/jpeg,image/webp,image/gif",
									style: { display: "none" },
									onChange: doFile,
									"aria-label": t("upload")
								}),
								react.createElement("button", { type: "button", style: primary, onClick: doPick, disabled: busy }, busy ? t("uploading") : t("upload"))
							)
						),

						// 透明度
						react.createElement("div", { style: row },
							react.createElement("span", { style: label }, t("opacity")),
							react.createElement("div", { style: { display: "flex", alignItems: "center", gap: "10px", flex: "1", minWidth: "0" } },
								react.createElement("input", {
									ref: rangeRef,
									type: "range",
									min: "0",
									max: "100",
									step: "1",
									value: String(opacityPercent),
									className: "wallpaper-range",
									style: range,
									"aria-label": t("opacity"),
									onChange: doOpacity
								}),
								react.createElement("span", { style: { ...label, flex: "none", minWidth: "34px", textAlign: "right" } }, opacityPercent + "%")
							)
						),

						// 契合度
						react.createElement("div", { style: row },
							react.createElement("span", { style: label }, t("fit")),
							react.createElement("select", { value: state.fit, style: select, onChange: doFit, "aria-label": t("fit") },
								react.createElement("option", { value: "cover" }, t("fit.cover")),
								react.createElement("option", { value: "contain" }, t("fit.contain")),
								react.createElement("option", { value: "stretch" }, t("fit.stretch"))
							)
						),

						// 恢复默认
						state.present
							? react.createElement("div", { style: { ...row, justifyContent: "flex-start" } },
								react.createElement("button", { type: "button", style: danger, onClick: doClear, disabled: busy }, t("restoreDefault"))
							)
							: null
					),

				state.error !== null ? react.createElement("p", { role: "alert", style: errorStyle }, state.error) : null,
				state.notice !== null ? react.createElement("p", { role: "status", style: noticeStyle }, state.notice) : null
			);
		}

		/**
		* 注册设置卡片 + 启动时应用已保存的壁纸。
		* @param ctx - 浏览器根上下文（slots + connection + locale）。
		* @returns 清理函数（插件卸载时移除壁纸层与样式覆盖）。
		*/
		function apply(ctx) {
			ctx.effect(() => ctx.locale.register(NS, {
				zh,
				en
			}), "ui-wallpaper: dictionaries");
			const t = ctx.locale.bind(NS);
			const injected = () => ({
				get: () => rpcCall(ctx, "wallpaper/get"),
				set: (data) => rpcCall(ctx, "wallpaper/set", { data }),
				update: (tuning) => rpcCall(ctx, "wallpaper/update", { tuning }),
				clear: () => rpcCall(ctx, "wallpaper/clear")
			});
			ctx.slots.inject("settings.general.item", () => ctx.slots.register({
				name: "settings.general.item",
				id: "wallpaper",
				order: 30,
				locale: NS,
				inject: injected
			}, WallpaperCard));
			ensureUiStyle();

			// 启动即恢复上次保存的壁纸（页面刷新 / 重启后仍生效）；
			// 定期兜底重应用，防止长时间运行中样式被外部改动。
			let mounted = true;
			const restore = () => {
				Promise.resolve().then(() => rpcCall(ctx, "wallpaper/get")).then(applyWallpaper).catch(() => {});
			};
			window.setTimeout(() => { if (mounted) restore(); }, 500);
			const timer = window.setInterval(() => { if (mounted) restore(); }, 60000);

			return () => {
				mounted = false;
				window.clearInterval(timer);
				removeWallpaper();
				if (uiStyleTag !== null) {
					uiStyleTag.remove();
					uiStyleTag = null;
				}
			};
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map
