// dsh-local-plugins — Host half.
//
// 本地插件管理：在 设置 → 插件 中新增「本地插件」页，可以
//   1. install(path)  从本地目录安装插件：校验 package.json → 复制到
//                     <profile>/node_modules/<name> → 在 cordis.patch.yml
//                     追加一行 insert（带 managed 标记）。
//   2. remove(id)     移除已安装插件：从 cordis.patch.yml 删掉对应行 →
//                     删除 node_modules 里的副本。
//   3. list()         列出当前 cordis.patch.yml 中用户安装的行 + 加载状态。
//
// 命令通过 Typert Gateway 的 SRC 模式暴露（localPluginManager/list、
// localPluginManager/install、localPluginManager/remove）：本服务继承
// TypertRemoteService 并用 Remote() 标记方法，网关自动认领这些端点。
// 浏览器端用 ctx.connection.rpc.call('/api', 'localPluginManager/...') 调用。
//
// 生效机制：写入 cordis.patch.yml 后，profile 的 patch watcher（boot 自动
// 挂载的 cordis-plugin-hmr）会热重载整棵 Loader 树——Host 半区立即生效；
// 浏览器端的新 bundle 需要刷新页面后才会被加载。
import { access, cp, mkdir, readFile, rm, stat, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { Remote, TypertRemoteService } from "@deepseek-ai/dsh-typert-protocol";

/** Stable Cordis plugin name (the loader row id). */
const name = "local-plugin-manager";
/** 本插件自身的包名（禁止从本管理器移除自己）。 */
const SELF_PACKAGE = "dsh-local-plugins";
/** Services required before the plugin mounts. */
const inject = [];
/** Managed-block marker seen in cordis.patch.yml comments. */
const MANAGED_MARKER = "managed by dsh-local-plugins";

/**
* 复制 @Remote 装饰器在编译产物里做的事情（本运行时不支持装饰器语法）：
* 用 Remote() + addInitializer 把标记写进 dsh-typert-protocol 的私有表。
* 返回实例初始化器，构造时以实例为 this 执行。
*/
function decorateRemote(ServiceClass, method) {
	const initializers = [];
	const context = {
		kind: "method",
		name: method,
		static: false,
		private: false,
		access: {
			has: (object) => method in object,
			get: (object) => object[method]
		},
		addInitializer(fn) {
			initializers.push(fn);
		},
		metadata: {}
	};
	Remote(ServiceClass.prototype[method], context);
	return initializers;
}

/** 行级解析 cordis.patch.yml：返回所有 insert 块（含行号，便于精确编辑且保留注释）。 */
function parseInsertBlocks(text) {
	const lines = text.split("\n");
	const blocks = [];
	let i = 0;
	while (i < lines.length) {
		const insertMatch = lines[i].match(/^(\s*)- insert:\s*$/);
		if (insertMatch === null) {
			i++;
			continue;
		}
		const indent = insertMatch[1];
		// managed 判定：上方相邻的注释行里是否带标记
		let managed = false;
		for (let k = i - 1; k >= 0 && lines[k].trim() !== ""; k--) {
			if (lines[k].trim().startsWith("#") && lines[k].includes(MANAGED_MARKER)) managed = true;
		}
		const rows = [];
		let j = i + 1;
		while (j < lines.length) {
			const line = lines[j];
			const rowMatch = line.match(/^(\s+)- id:\s*(.+?)\s*$/);
			if (rowMatch !== null && rowMatch[1].length > indent.length) {
				let name = null;
				let nameLine = -1;
				if (j + 1 < lines.length) {
					const nameMatch = lines[j + 1].match(/^\s+name:\s*(.+?)\s*$/);
					if (nameMatch !== null) {
						name = nameMatch[1].trim();
						nameLine = j + 1;
					}
				}
				rows.push({ id: unquote(rowMatch[2]), name: name === null ? null : unquote(name), idLine: j, nameLine });
				j = nameLine >= 0 ? nameLine + 1 : j + 1;
				continue;
			}
			// 块结束：同缩进或更浅的 "- " 顶层条目，或空白后紧跟顶层条目
			const topMatch = line.match(/^(\s*)- /);
			if (topMatch !== null && topMatch[1].length <= indent.length) break;
			if (line.trim() === "") {
				const look = j + 1;
				if (look < lines.length) {
					const lookMatch = lines[look].match(/^(\s*)- /);
					if (lookMatch !== null && lookMatch[1].length <= indent.length) break;
					if (/^\s*#/.test(lines[look]) && lines[look].match(/^\s*/)[0].length <= indent.length) break;
				}
			}
			// 顶层注释（下一个块的注释头）也结束当前块
			if (/^\s*#/.test(line) && line.match(/^\s*/)[0].length <= indent.length && rows.length > 0) break;
			j++;
		}
		blocks.push({ insertLine: i, indent, rows, endLine: j, managed });
		i = j;
	}
	return blocks;
}

function unquote(value) {
	const v = value.trim();
	if (v.length >= 2 && ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'")))) return v.slice(1, -1);
	return v;
}

/** 从 patch 文本中删除指定 id（或 name）的行；空块连同上方注释一起删除。 */
function removeRowFromPatch(text, targetId) {
	const lines = text.split("\n");
	const blocks = parseInsertBlocks(text);
	for (const block of blocks) {
		const row = block.rows.find((candidate) => candidate.id === targetId || candidate.name === targetId);
		if (row === void 0) continue;
		const remaining = block.rows.filter((candidate) => candidate !== row);
		if (remaining.length === 0) {
			let start = block.insertLine;
			// 只吞紧邻的注释行（块自己的注释头）
			while (start > 0 && lines[start - 1].trim().startsWith("#")) start--;
			// 再吞一个分隔空行；绝不继续越过（例如文件头注释）
			if (start > 0 && lines[start - 1].trim() === "") start--;
			lines.splice(start, block.endLine - start);
		} else {
			const removeLines = [row.idLine, row.nameLine].filter((n) => n >= 0).sort((a, b) => b - a);
			for (const lineIndex of removeLines) lines.splice(lineIndex, 1);
		}
		while (lines.length > 0 && lines[lines.length - 1].trim() === "") lines.pop();
		return { text: lines.join("\n") + (lines.length > 0 ? "\n" : ""), removed: true, name: row.name ?? row.id };
	}
	return { text, removed: false, name: null };
}

/**
* 本地插件管理服务。方法都是简单的单参数签名（SRC 模式按参数名解析）。
* @typert service localPluginManager
*/
class LocalPluginManagerService extends TypertRemoteService {
	constructor(ctx) {
		super(ctx, "localPluginManager");
		for (const initializer of REMOTE_INITIALIZERS) initializer.call(this);
	}

	/** 当前 profile 目录（Loader 的 baseUrl）。 */
	profileDir() {
		return fileURLToPath(this.ctx.baseUrl);
	}

	/** 列出用户安装在 cordis.patch.yml 里的插件及其加载状态。 */
	async list() {
		const profileDir = this.profileDir();
		const patchFile = join(profileDir, "cordis.patch.yml");
		const text = await readFile(patchFile, "utf8").catch(() => "");
		const blocks = parseInsertBlocks(text);
		const rows = blocks.flatMap((block) => block.rows.map((row) => ({ ...row, managed: block.managed })));
		const inventory = this.ctx.get("pluginInventory");
		const entries = inventory !== void 0 && typeof inventory.list === "function" ? inventory.list()?.entries ?? [] : [];
		const byName = new Map(entries.map((entry) => [entry.moduleName, entry]));
		const plugins = rows.map((row) => {
			const entry = byName.get(row.name);
			return {
				id: row.id,
				name: row.name,
				managed: row.managed,
				self: row.name === SELF_PACKAGE,
				enabled: entry === void 0 ? null : entry.enabled,
				fiberPhase: entry === void 0 ? null : entry.fiberPhase,
				present: existsSync(join(profileDir, "node_modules", row.name))
			};
		});
		return { profileDir, patchFile, plugins };
	}

	/**
	* 从本地目录安装一个 dsh 插件。
	* @param path - 插件目录的绝对路径（含 package.json）。
	* @returns 安装回执。
	*/
	async install(path) {
		const profileDir = this.profileDir();
		const patchFile = join(profileDir, "cordis.patch.yml");
		const source = String(path ?? "").trim();
		if (source === "") throw new Error("请提供插件目录路径");

		const sourceStat = await stat(source).catch(() => null);
		if (sourceStat === null || !sourceStat.isDirectory()) throw new Error("目录不存在或不可读：" + source);

		const pkgText = await readFile(join(source, "package.json"), "utf8").catch(() => null);
		if (pkgText === null) throw new Error("该目录缺少 package.json，不是有效的 dsh 插件");
		let pkg;
		try {
			pkg = JSON.parse(pkgText);
		} catch {
			throw new Error("package.json 无法解析（JSON 语法错误）");
		}
		const packageName = typeof pkg.name === "string" ? pkg.name : "";
		if (!/^[a-z0-9][a-z0-9._-]*$/.test(packageName) || packageName.startsWith(".") || packageName.startsWith("_")) {
			throw new Error("package.json 的 name 不是合法的 npm 包名：" + JSON.stringify(packageName));
		}
		if (packageName.startsWith("@")) throw new Error("暂不支持 @scope/name 形式的插件，请使用普通包名");
		if (packageName.startsWith("@deepseek-ai/") || packageName === SELF_PACKAGE) throw new Error("不允许安装内置插件同名/受保护的包：" + packageName);

		// 必须有可加载入口（loader 会 import 包根）
		const entryCandidates = [];
		if (typeof pkg.main === "string" && pkg.main !== "") entryCandidates.push(join(source, pkg.main));
		const dotExport = pkg.exports?.["."];
		if (typeof dotExport === "string") entryCandidates.push(join(source, dotExport));
		else if (dotExport !== null && typeof dotExport === "object" && typeof dotExport.default === "string") entryCandidates.push(join(source, dotExport.default));
		if (entryCandidates.length === 0) throw new Error("该插件缺少可加载入口（package.json 需要 main 或 exports['.']）");
		const entryExists = (await Promise.all(entryCandidates.map((candidate) => access(candidate).then(() => true).catch(() => false)))).some(Boolean);
		if (!entryExists) throw new Error("该插件声明的入口文件不存在：" + entryCandidates.join(" / "));

		// dsh.client 声明存在时，client bundle 必须存在
		const clientDecl = pkg.dsh !== null && typeof pkg.dsh === "object" ? pkg.dsh.client : void 0;
		if (clientDecl !== void 0) {
			const clientExport = pkg.exports?.["./client"];
			const clientPath = typeof clientExport === "string" ? clientExport : clientExport !== null && typeof clientExport === "object" ? clientExport.default : void 0;
			if (typeof clientPath !== "string") throw new Error("声明了 dsh.client 但 exports 缺少 './client'");
			const clientOk = await access(join(source, clientPath)).then(() => true).catch(() => false);
			if (!clientOk) throw new Error("dsh.client 声明的客户端文件不存在：" + clientPath);
		}

		// 不重复安装
		const target = join(profileDir, "node_modules", packageName);
		if (existsSync(target)) throw new Error("node_modules 中已存在同名插件：" + packageName);
		const patchText = await readFile(patchFile, "utf8").catch(() => "");
		if (parseInsertBlocks(patchText).some((block) => block.rows.some((row) => row.name === packageName))) {
			throw new Error("cordis.patch.yml 中已注册同名插件：" + packageName);
		}

		// 复制（跳过 .git；保留 node_modules 以便自包含依赖）
		await mkdir(join(profileDir, "node_modules"), { recursive: true });
		await cp(source, target, {
			recursive: true,
			filter: (candidate) => !candidate.split(/[\\/]/).includes(".git")
		});

		// 追加 managed 块
		const block = "\n# ── managed by dsh-local-plugins ──────────────────────────────────────────\n- insert:\n    - id: " + packageName + "\n      name: '" + packageName + "'\n";
		await writeFile(patchFile, patchText.replace(/\s+$/, "") + block, "utf8");

		return {
			installed: true,
			name: packageName,
			source,
			target,
			note: "已安装。Host 半区立即生效；含界面部分的插件需刷新页面后显示；若加载异常请重启服务。"
		};
	}


	/**
	* 只读检查一个插件目录：读取 package.json 的名称等信息，供安装前确认。
	* @param path - 插件目录的绝对路径。
	* @returns 包信息（不做任何写入）。
	*/
	async inspect(path) {
		const source = String(path ?? "").trim();
		if (source === "") throw new Error("请提供插件目录路径");
		const sourceStat = await stat(source).catch(() => null);
		if (sourceStat === null || !sourceStat.isDirectory()) throw new Error("目录不存在或不可读：" + source);
		const pkgText = await readFile(join(source, "package.json"), "utf8").catch(() => null);
		if (pkgText === null) throw new Error("该目录缺少 package.json，不是有效的 dsh 插件");
		let pkg;
		try {
			pkg = JSON.parse(pkgText);
		} catch {
			throw new Error("package.json 无法解析（JSON 语法错误）");
		}
		return {
			name: typeof pkg.name === "string" ? pkg.name : "",
			version: typeof pkg.version === "string" ? pkg.version : "",
			main: typeof pkg.main === "string" ? pkg.main : "",
			hasClient: !!(pkg.dsh !== null && typeof pkg.dsh === "object" && pkg.dsh.client)
		};
	}

	/**
	* 移除一个已安装插件（从 patch 文件删除行 + 删除 node_modules 副本）。
	* @param id - 插件行 id 或包名。
	* @returns 移除回执。
	*/
	async remove(id) {
		const profileDir = this.profileDir();
		const patchFile = join(profileDir, "cordis.patch.yml");
		const target = String(id ?? "").trim();
		if (target === "") throw new Error("请提供要移除的插件 id");
		if (target === SELF_PACKAGE || target === name) throw new Error("不能通过本管理器移除它自己：" + target);
		if (target.startsWith("@deepseek-ai/")) throw new Error("不允许移除内置插件：" + target);

		const text = await readFile(patchFile, "utf8").catch(() => "");
		const outcome = removeRowFromPatch(text, target);
		if (!outcome.removed) throw new Error("在 cordis.patch.yml 中找不到插件行：" + target);
		await writeFile(patchFile, outcome.text, "utf8");
		const moduleDir = join(profileDir, "node_modules", outcome.name);
		await rm(moduleDir, { recursive: true, force: true }).catch(() => {});
		return {
			removed: true,
			id: outcome.name,
			name: outcome.name,
			note: "已移除。Host 半区立即卸载；界面部分刷新页面后消失；重启服务可确保完全清理。"
		};
	}

	/**
	* 请求优雅重启 DSH 服务（launcher 的 appExit；桌面版 supervise 会自动拉起）。
	* @returns 重启回执（调用后进程即将退出，客户端应停止依赖响应）。
	*/
	async restart() {
		// appExit 由 launcher 注册在根 fiber：ctx.get 只查当前 store，必须用属性访问沿 fiber 链向上解析
		const exit = this.ctx.get("appExit") ?? this.ctx.appExit;
		if (typeof exit === "function") {
			// 优雅关停（dispose 整棵树后退出，上限 5s）
			queueMicrotask(() => {
				try {
					exit(0);
				} catch (error) {
					this.ctx.logger?.warn?.(`local-plugin-manager: graceful restart failed: ${String(error)}`);
				}
			});
		}
		// 硬兜底：无论优雅关停是否完成，4 秒后强制退出（桌面版 supervise 会自动拉起）
		const hard = setTimeout(() => {
			try {
				process.exit(0);
			} catch (error) {
				this.ctx.logger?.warn?.(`local-plugin-manager: hard restart failed: ${String(error)}`);
			}
		}, 4000);
		if (typeof hard.unref === "function") hard.unref();
		return { restarting: true, note: "正在重启 DSH…" };
	}
}

/** @Remote 标记的实例初始化器（构造时执行，等价于编译产物的装饰器）。 */
const REMOTE_INITIALIZERS = [
	...decorateRemote(LocalPluginManagerService, "list"),
	...decorateRemote(LocalPluginManagerService, "install"),
	...decorateRemote(LocalPluginManagerService, "inspect"),
	...decorateRemote(LocalPluginManagerService, "remove"),
	...decorateRemote(LocalPluginManagerService, "restart")
];

/**
* 挂载本地插件管理服务。
* @param ctx - host 上下文。
*/
function apply(ctx) {
	ctx.effect(() => {
		new LocalPluginManagerService(ctx);
	}, "local-plugin-manager: service");
}

export { LocalPluginManagerService, apply, inject, name };
