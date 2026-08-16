#!/usr/bin/env node
// patch-ui-workspace.js — apply or revert the dsh-client-ui-workspace menu patch
// needed by the dsh-session-delete plugin (rc.6 ships the session-row context
// menu hardcoded in the published bundle, with no extension point).
//
// Usage:
//   node patch-ui-workspace.js          # apply (idempotent)
//   node patch-ui-workspace.js --revert # revert
//
// The bundle is resolved the same way the running dsh resolves it: from the
// web profile's module anchor (fallback farm -> real npm-cache location).
import { createRequire } from "node:module";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { homedir } from "node:os";

const PROFILE = process.env.DSH_WEB_PROFILE ?? join(homedir(), ".dsh", "profiles", "web");
const require = createRequire(join(PROFILE, "index.js"));
let clientPath;
try {
	const pkg = require.resolve("@deepseek-ai/dsh-client-ui-workspace/package.json");
	clientPath = join(dirname(pkg), "lib", "client.js");
} catch {
	console.error("patch-ui-workspace: cannot resolve @deepseek-ai/dsh-client-ui-workspace from " + PROFILE);
	process.exit(1);
}

const EDITS = [
	{
		old: `				{
					id: "archive",
					label: t("menu.archiveSession"),
					icon: (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconArchiveOutline20, { size: 16 })
				}
			];`,
		new: `				{
					id: "archive",
					label: t("menu.archiveSession"),
					icon: (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconArchiveOutline20, { size: 16 })
				},
				...(window.__DSH_SESSION_DELETE__ === true ? [{
					id: "delete",
					label: t("menu.deleteSession"),
					icon: (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconTrashOutline16, {})
				}] : [])
			];`
	},
	{
		old: `								onSelect: (id) => {
									setMenuOpen(false);
									if (id === "rename") onRename(node.id, row.title);
									if (id === "fork") onFork(node.id);
									if (id === "archive") onArchive(node.id);
								},`,
		new: `								onSelect: (id) => {
									setMenuOpen(false);
									if (id === "rename") onRename(node.id, row.title);
									if (id === "fork") onFork(node.id);
									if (id === "archive") onArchive(node.id);
									if (id === "delete") {
										window.dispatchEvent(new CustomEvent("dsh:session-delete", { detail: { sessionId: node.id, title: row.title } }));
									}
								},`
	},
	{
		old: `			"menu.fork": "分叉会话",
			"menu.archiveSession": "归档会话",`,
		new: `			"menu.fork": "分叉会话",
			"menu.archiveSession": "归档会话",
			"menu.deleteSession": "删除会话",`
	},
	{
		old: `			"menu.fork": "Fork session",
			"menu.archiveSession": "Archive session",`,
		new: `			"menu.fork": "Fork session",
			"menu.archiveSession": "Archive session",
			"menu.deleteSession": "Delete session",`
	}
];

const revert = process.argv.includes("--revert");
let source = readFileSync(clientPath, "utf8");
let changed = 0;
for (const edit of EDITS) {
	if (!revert) {
		if (source.includes(edit.new)) continue; // already applied
		if (!source.includes(edit.old)) {
			console.error("patch-ui-workspace: anchor not found (bundle version changed?): " + JSON.stringify(edit.old.slice(0, 60)) + "…");
			process.exit(2);
		}
		source = source.replace(edit.old, edit.new);
	} else {
		if (!source.includes(edit.new)) continue; // already reverted
		source = source.replace(edit.new, edit.old);
	}
	changed++;
}
writeFileSync(clientPath, source);
console.log((revert ? "reverted" : "applied") + " " + changed + " of " + EDITS.length + " edits in " + clientPath);
