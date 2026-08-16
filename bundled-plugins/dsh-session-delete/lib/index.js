// dsh-session-delete — Host half.
//
// Registers the "/delete-session" human command. The browser half (client.js)
// invokes it through the commands Remote (commands/execute) addressed at the
// session being deleted; the command therefore always runs on the target
// session's own agent (invocation.agent).
//
// What the handler does, in order:
//   1. cancels a running turn (user-initiated cancellation) and waits for the
//      agent to reach quiescence,
//   2. flushes the session log through the durable persistence barrier,
//   3. archives the session in the workspace registry — the client hides an
//      archived session from every sidebar surface immediately, so the row
//      disappears right away (this plugin's removal UX does not depend on a
//      host-side session-disposal API, which this deployment does not expose),
//   4. detaches the session from every workspace account it belongs to,
//   5. moves the session's persisted artifact into
//      <DSH_HOME>/trash/<yyyy-mm-dd>/<session-id>/ — the same recoverable
//      trash layout the Harness home already uses, so the log is recoverable
//      until the trash directory is cleared.
//
// The artifact move makes the session disappear from session.list on the next
// baseline refresh; archiving makes it disappear from the sidebar immediately;
// on the next app start the session is gone completely (only the stale
// archive-set id remains, which is never rendered for a session that no
// longer exists).
import { mkdir, rename } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, join } from "node:path";

/** Stable Cordis plugin name (the loader row id). */
const name = "session-delete";
/** Services required before the plugin mounts. */
const inject = ["commands", "sessions"];

/**
* Resolve the DeepSeek Harness home (same precedence as the platform's
* dsh-home-paths helper: $DSH_HOME, then ~/.dsh). Inlined so this plugin is
* self-contained and loadable from any plugin directory without depending on
* an @deepseek-ai package resolving through its real location.
* @param segments - path segments appended to the Harness home.
* @returns the normalized absolute joined path.
*/
function dshHomePath(...segments) {
	const env = process.env.DSH_HOME;
	const root = env !== void 0 && env.trim().length > 0 ? env.trim() : join(homedir(), ".dsh");
	return join(root, ...segments);
}

/** Current local date as yyyy-mm-dd for the trash bucket. */
function trashBucketDate() {
	const now = new Date();
	const y = now.getFullYear();
	const m = String(now.getMonth() + 1).padStart(2, "0");
	const d = String(now.getDate()).padStart(2, "0");
	return `${y}-${m}-${d}`;
}

/**
 * Move one session's persisted artifact directory into the Harness trash.
 * Returns a short human-readable report, or null when the session had no
 * persisted artifact (a blank, never-written session).
 */
async function moveArtifactToTrash(ctx, sessionId) {
	const persistence = ctx.get("sessionPersistence");
	if (persistence === void 0) return null;
	const metas = await persistence.list();
	const meta = metas.find((candidate) => candidate.id === sessionId);
	if (meta === void 0) return null;
	const location = persistence.locate(meta);
	if (location === void 0) return null;
	const sessionDir = dirname(location.path);
	const targetRoot = dshHomePath("trash", trashBucketDate());
	await mkdir(targetRoot, { recursive: true });
	const target = `${targetRoot}/${sessionId}`;
	await rename(sessionDir, target);
	return `${target}`;
}

/**
 * Execute one delete request for the command's own session.
 * @param ctx - host context.
 * @param invocation - the slash-command invocation (its agent owns the session).
 * @returns the human-command result.
 */
async function executeDelete(ctx, invocation) {
	const agent = invocation.agent;
	const session = agent.session;
	const sessionId = session.id;
	try {
		if (agent.status === "running") {
			agent.cancel({ kind: "user" });
			await agent.whenIdle();
		}
		await ctx.sessions.flush(session);

		const registry = ctx.get("workspaceRegistry");
		if (registry !== void 0) {
			// Archive first: the sidebar hides archived sessions immediately.
			await registry.archiveSession(sessionId);
			// Detach from every workspace account the session belongs to.
			for (const workspace of registry.list()) {
				if (workspace.sessionIds.includes(sessionId)) {
					await workspace.detachSession(sessionId);
				}
			}
		}

		let trashPath = null;
		try {
			trashPath = await moveArtifactToTrash(ctx, sessionId);
		} catch (error) {
			ctx.logger.warn(`session-delete: artifact move failed for ${sessionId}: ${String(error)}`);
		}

		const parts = [`会话 ${sessionId} 已删除。`];
		if (trashPath !== null) parts.push(`日志已移入回收站：${trashPath}`);
		else parts.push("会话没有可移动的日志文件（空会话）。");
		if (trashPath === null && (await ctx.get("sessionPersistence")) !== void 0) parts.push("注意：若日志文件仍存在，重启后该会话可能重新出现。");
		return { kind: "success", text: parts.join(" ") };
	} catch (error) {
		ctx.logger.warn(`session-delete: deleting ${sessionId} failed: ${String(error)}`);
		return {
			kind: "error",
			text: `删除会话失败：${error instanceof Error ? error.message : String(error)}`
		};
	}
}

/**
 * Register the "/delete-session" command for every human-command adapter.
 * @param ctx - host context carrying the command registry.
 */
function apply(ctx) {
	ctx.effect(() => ctx.commands.register({
		name: "delete-session",
		description: "Delete the current conversation (moves its log to the trash)",
		handler: (invocation) => executeDelete(ctx, invocation)
	}), "session-delete: command");
}

export { apply, inject, name };
