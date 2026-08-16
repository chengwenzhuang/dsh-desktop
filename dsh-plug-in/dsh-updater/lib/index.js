// dsh-updater — Host half.
//
// 软件更新中继：桌面端（DSH.exe 的 updater）负责真实的 GitHub 检查 / 下载 /
// 替换 / 重启；本服务只做两件事：
//   status()  读取桌面端写入的状态文件（%LOCALAPPDATA%/DSH/update/state.json）；
//   check() / apply()  向 request.json 写入请求，桌面端轮询消费。
// 端点经 Typert Gateway SRC 模式暴露为 /api/updater/*。
import { readFile, writeFile, mkdir } from "node:fs/promises";
import { join } from "node:path";
import { homedir } from "node:os";
import { Remote, TypertRemoteService } from "@deepseek-ai/dsh-typert-protocol";

/** Stable Cordis plugin name. */
const name = "updater";
/** Services required before the plugin mounts. */
const inject = [];

/** 与桌面端一致的更新目录：%LOCALAPPDATA%/DSH/update。 */
function updateDir() {
	const local = process.env.LOCALAPPDATA ?? join(homedir(), "AppData", "Local");
	return join(local, "DSH", "update");
}

async function readStateFile() {
	try {
		const text = await readFile(join(updateDir(), "state.json"), "utf8");
		return JSON.parse(text);
	} catch {
		return null;
	}
}

async function writeRequest(request) {
	const dir = updateDir();
	await mkdir(dir, { recursive: true });
	const tmp = join(dir, "request.json.tmp");
	await writeFile(tmp, JSON.stringify({ request }), "utf8");
	const { rename } = await import("node:fs/promises");
	await rename(tmp, join(dir, "request.json"));
}

function decorateRemote(ServiceClass, method) {
	const initializers = [];
	const context = {
		kind: "method",
		name: method,
		static: false,
		private: false,
		access: { has: (o) => method in o, get: (o) => o[method] },
		addInitializer(fn) { initializers.push(fn); },
		metadata: {}
	};
	Remote(ServiceClass.prototype[method], context);
	return initializers;
}

class UpdaterService extends TypertRemoteService {
	constructor(ctx) {
		super(ctx, "updater");
		for (const initializer of REMOTE_INITIALIZERS) initializer.call(this);
	}

	/** 读取桌面端更新状态。 */
	async status() {
		const state = await readStateFile();
		if (state === null) {
			return { present: false, status: "unconfigured", currentVersion: "", latestVersion: null, latestUrl: null, publishedAt: null, error: "未检测到桌面更新组件（DSH.exe updater）" };
		}
		return { present: true, ...state };
	}

	/** 请求桌面端立即检测更新。 */
	async check() {
		await writeRequest("check");
		return { requested: true };
	}

	/** 请求桌面端下载并应用更新（下载→替换 exe→自动重启）。 */
	async apply() {
		await writeRequest("apply");
		return { requested: true };
	}
}

const REMOTE_INITIALIZERS = [
	...decorateRemote(UpdaterService, "status"),
	...decorateRemote(UpdaterService, "check"),
	...decorateRemote(UpdaterService, "apply")
];

function apply(ctx) {
	ctx.effect(() => {
		new UpdaterService(ctx);
	}, "updater: service");
}

export { UpdaterService, apply, inject, name };
