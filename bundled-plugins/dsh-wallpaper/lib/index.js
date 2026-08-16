// dsh-wallpaper — Host half.
//
// 背景壁纸：把用户上传的图片保存到 <DSH_HOME>/wallpaper/，并持久化
// 不透明度（opacity）与契合度（fit）设置；浏览器半区（client.js）读取后
// 渲染为应用背景，并提供「设置 → 通用」的「背景壁纸」卡片（上传 / 调节 /
// 恢复默认）。
//
// 端点经 Typert Gateway SRC 模式暴露为 /api/wallpaper/*：
//   get()               读取当前壁纸（含 data URL 与设置）；未设置时 present:false
//   set(data)           上传新壁纸（data = { image: dataURL, name: 原文件名 }），
//                       保留原不透明度/契合度
//   update(tuning)      只更新设置（tuning = { opacity: 0..1, fit }）
//   clear()             删除壁纸与设置，恢复默认背景
import { mkdir, readFile, rename, unlink, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { homedir } from "node:os";
import { Remote, TypertRemoteService } from "@deepseek-ai/dsh-typert-protocol";

/** Stable Cordis plugin name (the loader row id). */
const name = "wallpaper";
/** Services required before the plugin mounts. */
const inject = [];

/** 允许的图片格式：MIME → 扩展名。 */
const ALLOWED_IMAGE = {
	"image/png": "png",
	"image/jpeg": "jpg",
	"image/webp": "webp",
	"image/gif": "gif"
};
/** 单张壁纸上限（解码后字节数）。 */
const MAX_IMAGE_BYTES = 15 * 1024 * 1024;
/** 契合度（background-size）允许值。 */
const FITS = ["cover", "contain", "stretch"];
/** 默认透明度（0..1）。 */
const DEFAULT_OPACITY = 0.6;
/** 默认契合度。 */
const DEFAULT_FIT = "cover";

/**
 * DeepSeek Harness 数据根目录（$DSH_HOME，否则 ~/.dsh），与 dsh-session-delete
 * 的解析优先级一致，保证插件自包含、可从任意插件目录加载。
 * @returns 规范化后的绝对路径。
 */
function dshHomeRoot() {
	const env = process.env.DSH_HOME;
	const root = env !== void 0 && env.trim().length > 0 ? env.trim() : join(homedir(), ".dsh");
	return root;
}

/** 壁纸数据目录（<DSH_HOME>/wallpaper）。 */
function wallpaperDir() {
	return join(dshHomeRoot(), "wallpaper");
}

/** 读设置；无设置或损坏时返回 null（视为未设置，绝不抛错）。 */
async function readSettings() {
	try {
		const text = await readFile(join(wallpaperDir(), "settings.json"), "utf8");
		return JSON.parse(text);
	} catch {
		return null;
	}
}

/** 原子写设置（先写 tmp 再 rename）。 */
async function writeSettings(settings) {
	const dir = wallpaperDir();
	await mkdir(dir, { recursive: true });
	const tmp = join(dir, "settings.json.tmp");
	await writeFile(tmp, JSON.stringify(settings, null, 2), "utf8");
	await rename(tmp, join(dir, "settings.json"));
}

/**
 * 解析并校验图片 data URL。
 * @param dataUrl - "data:<mime>;base64,...." 形式的字符串。
 * @returns 解码后的 buffer、扩展名与 mime。
 * @throws 格式不支持 / 内容为空 / 超过大小限制时抛中文错误。
 */
function parseImageDataUrl(dataUrl) {
	const match = /^data:([^;,]+);base64,(.*)$/s.exec(String(dataUrl ?? ""));
	if (match === null) throw new Error("不是有效的图片 data URL");
	const mime = match[1].toLowerCase();
	const ext = ALLOWED_IMAGE[mime];
	if (ext === void 0) throw new Error("不支持的图片格式：" + mime + "（支持 png / jpeg / webp / gif）");
	const buffer = Buffer.from(match[2], "base64");
	if (buffer.length === 0) throw new Error("图片内容为空");
	if (buffer.length > MAX_IMAGE_BYTES) throw new Error("图片超过 15MB 限制");
	return { buffer, ext, mime };
}

/**
 * 由设置组装返回给客户端的壁纸状态（data URL + 当前设置）。
 * 图片文件缺失时视为未设置。
 * @param settings - readSettings() 的返回值（可为 null）。
 * @returns { present, image?, opacity?, fit? }
 */
async function stateFromSettings(settings) {
	if (settings === null || typeof settings.image !== "string") return { present: false };
	try {
		const buffer = await readFile(join(wallpaperDir(), settings.image));
		return {
			present: true,
			image: `data:${typeof settings.mime === "string" ? settings.mime : "image/png"};base64,${buffer.toString("base64")}`,
			opacity: typeof settings.opacity === "number" ? settings.opacity : DEFAULT_OPACITY,
			fit: FITS.includes(settings.fit) ? settings.fit : DEFAULT_FIT
		};
	} catch {
		return { present: false };
	}
}

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

/**
 * 背景壁纸服务。方法都是简单的单参数签名（SRC 模式按参数名解析）。
 * @typert service wallpaper
 */
class WallpaperService extends TypertRemoteService {
	constructor(ctx) {
		super(ctx, "wallpaper");
		for (const initializer of REMOTE_INITIALIZERS) initializer.call(this);
	}

	/** 读取当前壁纸与设置；未设置时返回 { present: false }。 */
	async get() {
		return stateFromSettings(await readSettings());
	}

	/**
	 * 上传新壁纸（保留原不透明度/契合度）。
	 * @param data - { image: dataURL, name: 原文件名（仅记录，不用于落盘名） }。
	 * @returns 更新后的壁纸状态。
	 */
	async set(data) {
		const payload = data !== null && typeof data === "object" ? data : {};
		const { buffer, ext, mime } = parseImageDataUrl(payload.image);
		const dir = wallpaperDir();
		await mkdir(dir, { recursive: true });
		const imageName = "wallpaper." + ext;
		const tmp = join(dir, imageName + ".tmp");
		await writeFile(tmp, buffer);
		await rename(tmp, join(dir, imageName));
		const previous = await readSettings();
		const settings = {
			image: imageName,
			mime,
			opacity: previous !== null && typeof previous.opacity === "number" ? previous.opacity : DEFAULT_OPACITY,
			fit: previous !== null && FITS.includes(previous.fit) ? previous.fit : DEFAULT_FIT
		};
		await writeSettings(settings);
		return stateFromSettings(settings);
	}

	/**
	 * 仅更新显示设置（壁纸已存在时）。
	 * @param tuning - { opacity: 0..1, fit: cover|contain|stretch }，缺省字段保持不变。
	 * @returns 更新后的壁纸状态。
	 */
	async update(tuning) {
		const settings = await readSettings();
		if (settings === null || typeof settings.image !== "string") throw new Error("尚未设置壁纸");
		const tuningObject = tuning !== null && typeof tuning === "object" ? tuning : {};
		if (typeof tuningObject.opacity === "number" && tuningObject.opacity >= 0 && tuningObject.opacity <= 1) {
			settings.opacity = tuningObject.opacity;
		}
		if (FITS.includes(tuningObject.fit)) settings.fit = tuningObject.fit;
		await writeSettings(settings);
		return stateFromSettings(settings);
	}

	/** 删除壁纸与设置，恢复默认背景。 */
	async clear() {
		const dir = wallpaperDir();
		const settings = await readSettings();
		if (settings !== null && typeof settings.image === "string") {
			await unlink(join(dir, settings.image)).catch(() => {});
		}
		await unlink(join(dir, "settings.json")).catch(() => {});
		return { present: false };
	}
}

/** @Remote 标记的实例初始化器（构造时执行，等价于编译产物的装饰器）。 */
const REMOTE_INITIALIZERS = [
	...decorateRemote(WallpaperService, "get"),
	...decorateRemote(WallpaperService, "set"),
	...decorateRemote(WallpaperService, "update"),
	...decorateRemote(WallpaperService, "clear")
];

/**
 * 挂载背景壁纸服务。
 * @param ctx - host 上下文。
 */
function apply(ctx) {
	ctx.effect(() => {
		new WallpaperService(ctx);
	}, "wallpaper: service");
}

export { WallpaperService, apply, inject, name };
