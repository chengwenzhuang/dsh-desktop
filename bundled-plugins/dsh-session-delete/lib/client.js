window.__ModuleLoader__.load({
	id: "dsh-session-delete",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		// 标记插件已加载：ui-workspace 补丁据此条件渲染「删除会话」菜单项，
		// 移除本插件并刷新页面后该标志消失，菜单项随之隐藏。
		window.__DSH_SESSION_DELETE__ = true;
		//#region dsh-session-delete client half
		/**
		* Browser half of the "delete conversation" plugin.
		*
		* The session-row context menu (three dots) in dsh-client-ui-workspace is
		* patched to emit a window CustomEvent named "dsh:session-delete" with
		* { sessionId, title } when its "删除会话" item is chosen. This plugin owns
		* everything after that: a confirmation dialog, the host call through the
		* commands Remote (ctx.remote.commands.execute -> /delete-session on the
		* target session's own agent), and the list upkeep (clear the selection
		* when the current session was deleted, then refresh the baseline).
		*
		* The dialog is rendered with plain DOM + the web theme's CSS variables
		* so this bundle has no cross-plugin value dependencies.
		*/
		/** Services required before the plugin mounts. */
		const inject = ["remote", "remote.commands", "sessions"];

		/** Tiny inline locale seat (the sidebar runs zh-CN by default). */
		function isZh() {
			const lang = (typeof document !== "undefined" && document.documentElement.lang) || (typeof navigator !== "undefined" && navigator.language) || "";
			return /^zh/i.test(lang);
		}
		function t(zhText, enText) {
			return isZh() ? zhText : enText;
		}

		let overlay = null;
		let card = null;
		let busyEl = null;
		let errorEl = null;
		let state = { busy: false, error: null };

		function setState(patch) {
			state = { ...state, ...patch };
			renderState();
		}

		function renderState() {
			if (busyEl !== null) busyEl.style.display = state.busy ? "" : "none";
			if (errorEl !== null) {
				errorEl.style.display = state.error ? "" : "none";
				errorEl.textContent = state.error ?? "";
			}
		}

		/** Build and show the confirmation overlay for one session. */
		function openConfirm(ctx, sessionId, title) {
			if (overlay !== null) closeDialog();
			state = { busy: false, error: null };
			const safeTitle = typeof title === "string" && title.length > 0 ? title : sessionId;

			// Root: full-screen flex seat (mirrors the platform Modal).
			overlay = document.createElement("div");
			overlay.style.cssText = [
				"position:fixed",
				"inset:0",
				"z-index:1000",
				"display:flex",
				"align-items:center",
				"justify-content:center",
				"padding:24px",
				"box-sizing:border-box",
				"font-family:var(--dsh-font-family, system-ui, -apple-system, 'Segoe UI', sans-serif)"
			].join(";");

			// Mask: themed overlay + blur, exactly like the platform Modal.
			const mask = document.createElement("div");
			mask.style.cssText = [
				"position:absolute",
				"inset:0",
				"background:var(--dsw-alias-bg-mask-1, rgba(0,0,0,.24))",
				"-webkit-backdrop-filter:var(--dsw-mask-blur, blur(4px))",
				"backdrop-filter:var(--dsw-mask-blur, blur(4px))"
			].join(";");
			mask.addEventListener("click", () => {
				if (!state.busy) closeDialog();
			});
			overlay.appendChild(mask);

			// Dialog card: themed raised surface.
			card = document.createElement("div");
			card.setAttribute("role", "dialog");
			card.setAttribute("aria-modal", "true");
			card.style.cssText = [
				"position:relative",
				"z-index:1",
				"display:flex",
				"flex-direction:column",
				"gap:14px",
				"width:min(440px,100%)",
				"box-sizing:border-box",
				"padding:22px 24px 24px",
				"border:1px solid var(--dsw-alias-border-inverted, rgba(0,0,0,.1))",
				"border-radius:24px",
				"background:var(--dsw-alias-bg-layer-2, #fff)",
				"box-shadow:var(--dsw-shadow-lv3, 0 8px 30px rgba(0,0,0,.2))",
				"color:var(--dsw-alias-label-primary, #222)"
			].join(";");

			const heading = document.createElement("div");
			heading.style.cssText = "margin:0;font-size:16px;line-height:24px;font-weight:500;color:var(--dsw-alias-label-primary, #222)";
			heading.textContent = t("删除会话", "Delete Session");

			const body = document.createElement("div");
			body.style.cssText = "margin:0;font-size:14px;line-height:22px;color:var(--dsw-alias-label-primary, #333);white-space:pre-line;word-break:break-all";
			body.textContent = t(
				`确定要删除会话「${safeTitle}」吗？
会话日志将移入回收站，删除后无法从会话列表恢复。若会话正在运行，会先停止它。`,
				`Delete the conversation "${safeTitle}"? Its log will be moved to the trash and cannot be restored from the list. A running session is stopped first.`
			);

			busyEl = document.createElement("div");
			busyEl.setAttribute("role", "status");
			busyEl.style.cssText = "font-size:13px;line-height:20px;color:var(--dsw-alias-label-secondary, #666)";
			busyEl.textContent = t("正在删除…", "Deleting…");

			errorEl = document.createElement("div");
			errorEl.setAttribute("role", "alert");
			errorEl.style.cssText = "font-size:13px;line-height:20px;color:var(--dsw-alias-state-error-primary, #d33)";

			const footer = document.createElement("div");
			footer.style.cssText = "display:flex;align-items:center;justify-content:flex-end;gap:8px;margin-top:2px";

			const cancelBtn = makeButton(t("取消", "Cancel"), "outline");
			cancelBtn.addEventListener("click", () => closeDialog());

			const deleteBtn = makeButton(t("删除", "Delete"), "danger");
			deleteBtn.addEventListener("click", () => {
				if (state.busy) return;
				confirmDelete(ctx, sessionId, deleteBtn);
			});

			footer.append(cancelBtn, deleteBtn);
			card.append(heading, body, busyEl, errorEl, footer);
			overlay.appendChild(card);
			document.body.appendChild(overlay);
			renderState();

			const onKey = (event) => {
				if (event.key === "Escape" && !state.busy) closeDialog();
			};
			window.addEventListener("keydown", onKey);
			overlay._onKey = onKey;
		}

		/** Build a themed button. */
		function makeButton(label, variant) {
			const el = document.createElement("button");
			el.type = "button";
			el.textContent = label;
			const base = [
				"height:32px",
				"padding:0 14px",
				"border-radius:8px",
				"font-size:13px",
				"font-weight:500",
				"cursor:pointer",
				"font-family:inherit"
			];
			if (variant === "danger") {
				base.push(
					"background:var(--dsw-alias-state-error-primary, #d33)",
					"border:1px solid transparent",
					"color:#fff"
				);
			} else {
				base.push(
					"background:transparent",
					"border:1px solid var(--dsw-alias-border-l2, rgba(0,0,0,.2))",
					"color:var(--dsw-alias-label-primary, #222)"
				);
			}
			el.style.cssText = base.join(";");
			return el;
		}

		/** Close the dialog and release its listeners. */
		function closeDialog() {
			if (overlay !== null) {
				if (overlay._onKey) window.removeEventListener("keydown", overlay._onKey);
				overlay.remove();
			}
			overlay = null;
			card = null;
			busyEl = null;
			errorEl = null;
			state = { busy: false, error: null };
		}

		/**
		* Run the delete: invoke /delete-session on the target session's own
		* agent through the commands Remote, then keep the sidebar consistent.
		*/
		async function confirmDelete(ctx, sessionId, deleteBtn) {
			if (state.busy) return;
			setState({ busy: true, error: null });
			try {
				const result = await ctx.remote.commands.execute(sessionId, "/delete-session");
				if (!result.ok) throw new Error(result.error?.message ?? "delete-session failed");
				const value = result.value;
				if (value === void 0 || value === null) throw new Error(t("删除命令未执行（命令不存在？）", "The delete command did not run (unknown command?)."));
				if (value.result && value.result.kind === "error") throw new Error(value.result.text ?? "delete-session failed");
				// Success: clear the selection when the current session was deleted,
				// then refresh the session baseline.
				const list = ctx.sessions.list.getSnapshot();
				if (list.current === sessionId) ctx.sessions.clear();
				await ctx.sessions.refresh();
				closeDialog();
			} catch (error) {
				const message = error instanceof Error ? error.message : String(error);
				setState({ busy: false, error: message });
			}
		}

		/**
		* Plugin entry: subscribe to the sidebar's delete request event.
		* @param ctx - client root context (remote + sessions injected).
		* @returns disposer.
		*/
		function apply(ctx) {
			const onDeleteRequest = (event) => {
				const detail = event.detail;
				if (detail === void 0 || detail === null || typeof detail.sessionId !== "string") return;
				openConfirm(ctx, detail.sessionId, typeof detail.title === "string" ? detail.title : "");
			};
			window.addEventListener("dsh:session-delete", onDeleteRequest);
			return () => {
				window.removeEventListener("dsh:session-delete", onDeleteRequest);
				closeDialog();
			};
		}
		//#endregion
		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});

//# sourceMappingURL=client.js.map
