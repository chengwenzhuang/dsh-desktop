package main

import (
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

//go:embed bundled-plugins
var bundledPluginsFS embed.FS

// bundledPluginDef describes one plugin shipped inside this exe: the embedded
// directory (relative to the package root) and the loader row to register in
// the web profile's cordis.patch.yml.
type bundledPluginDef struct {
	dir  string
	id   string
	name string
}

var bundledPlugins = []bundledPluginDef{
	{dir: "bundled-plugins/dsh-local-plugins", id: "local-plugin-manager", name: "dsh-local-plugins"},
	{dir: "bundled-plugins/dsh-session-delete", id: "session-delete", name: "dsh-session-delete"},
	{dir: "bundled-plugins/dsh-updater", id: "updater", name: "dsh-updater"},
}

// dshDataHome resolves the DeepSeek Harness home ($DSH_HOME, else ~/.dsh),
// mirroring the platform's dsh-home-paths precedence.
func dshDataHome() string {
	if env := os.Getenv("DSH_HOME"); strings.TrimSpace(env) != "" {
		return strings.TrimSpace(env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".dsh")
	}
	return filepath.Join(home, ".dsh")
}

// dshInstallRoot resolves the dsh installation root from its bin path
// (<root>/node_modules/@deepseek-ai/dsh/lib/bin.js -> <root>).
func dshInstallRoot(bin string) string {
	return filepath.Clean(filepath.Join(filepath.Dir(bin), "..", "..", "..", ".."))
}

// copyEmbeddedDir extracts one embedded directory tree to dest (files only).
func copyEmbeddedDir(fsys fs.FS, dir, dest string) error {
	return fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dest, strings.TrimPrefix(p, dir)), 0o755)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, dir)
		out := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
}

// applyStringPatch replaces oldText with newText when oldText is present and
// newText is not yet present. Returns whether the file changed.
func applyStringPatch(content *string, oldText, newText string) bool {
	if strings.Contains(*content, newText) {
		return false // already applied
	}
	if !strings.Contains(*content, oldText) {
		return false // anchor missing (different bundle version) — skip silently
	}
	*content = strings.Replace(*content, oldText, newText, 1)
	return true
}

// patchUIWorkspace adds the 删除会话 menu item (conditional on
// window.__DSH_SESSION_DELETE__) to the shipped dsh-client-ui-workspace client
// bundle. The menu is hardcoded in the published bundle, so the delete plugin
// needs this compatibility patch. Idempotent; a missing/mismatched bundle is
// skipped without failing the app.
func patchUIWorkspace(installRoot string) {
	clientPath := filepath.Join(installRoot, "node_modules", "@deepseek-ai", "dsh-client-ui-workspace", "lib", "client.js")
	data, err := os.ReadFile(clientPath)
	if err != nil {
		log.Printf("bundled-plugins: ui-workspace bundle not found at %s: %v", clientPath, err)
		return
	}
	content := string(data)
	changed := false

	// 1) menu item: conditional 删除会话 after 归档会话
	changed = applyStringPatch(&content,
		"\t\t\t\t{\n\t\t\t\t\tid: \"archive\",\n\t\t\t\t\tlabel: t(\"menu.archiveSession\"),\n\t\t\t\t\ticon: (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconArchiveOutline20, { size: 16 })\n\t\t\t\t}\n\t\t\t];",
		"\t\t\t\t{\n\t\t\t\t\tid: \"archive\",\n\t\t\t\t\tlabel: t(\"menu.archiveSession\"),\n\t\t\t\t\ticon: (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconArchiveOutline20, { size: 16 })\n\t\t\t\t},\n\t\t\t\t...(window.__DSH_SESSION_DELETE__ === true ? [{\n\t\t\t\t\tid: \"delete\",\n\t\t\t\t\tlabel: t(\"menu.deleteSession\"),\n\t\t\t\t\ticon: (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconTrashOutline16, {})\n\t\t\t\t}] : [])\n\t\t\t];") || changed

	// 2) onSelect: dispatch the delete request event
	changed = applyStringPatch(&content,
		"\t\t\t\t\t\t\t\t\tif (id === \"archive\") onArchive(node.id);\n\t\t\t\t\t\t\t\t},",
		"\t\t\t\t\t\t\t\t\tif (id === \"archive\") onArchive(node.id);\n\t\t\t\t\t\t\t\t\tif (id === \"delete\") {\n\t\t\t\t\t\t\t\t\t\twindow.dispatchEvent(new CustomEvent(\"dsh:session-delete\", { detail: { sessionId: node.id, title: row.title } }));\n\t\t\t\t\t\t\t\t\t}\n\t\t\t\t\t\t\t\t}") || changed

	// 3) zh dictionary key
	changed = applyStringPatch(&content,
		"\"menu.archiveSession\": \"归档会话\",",
		"\"menu.archiveSession\": \"归档会话\",\n\t\t\t\"menu.deleteSession\": \"删除会话\",") || changed

	// 4) en dictionary key
	changed = applyStringPatch(&content,
		"\"menu.archiveSession\": \"Archive session\",",
		"\"menu.archiveSession\": \"Archive session\",\n\t\t\t\"menu.deleteSession\": \"Delete session\",") || changed

	if changed {
		if err := os.WriteFile(clientPath, []byte(content), 0o644); err != nil {
			log.Printf("bundled-plugins: writing ui-workspace patch failed: %v", err)
			return
		}
		log.Printf("bundled-plugins: patched ui-workspace menu at %s", clientPath)
	} else {
		log.Printf("bundled-plugins: ui-workspace bundle already patched (or anchors missing)")
	}
}

// ensureBundledPlugins installs the shipped plugins into the web profile
// before the server boots: copies each plugin into
// <home>/profiles/web/node_modules/<name>, registers its loader row in
// cordis.patch.yml, and applies the ui-workspace menu patch needed by the
// delete plugin. Idempotent — existing copies/rows/patches are never
// touched, and an already-initialized profile is left intact (the profile
// template only writes missing files). Failures are logged and non-fatal.
func ensureBundledPlugins(bin string) {
	profileDir := filepath.Join(dshDataHome(), "profiles", "web")
	nmDir := filepath.Join(profileDir, "node_modules")
	if err := os.MkdirAll(nmDir, 0o755); err != nil {
		log.Printf("bundled-plugins: cannot create %s: %v", nmDir, err)
		return
	}

	// 1) copy each bundled plugin if missing
	for _, p := range bundledPlugins {
		target := filepath.Join(nmDir, p.name)
		if fileExists(filepath.Join(target, "package.json")) {
			continue
		}
		if err := copyEmbeddedDir(bundledPluginsFS, p.dir, target); err != nil {
			log.Printf("bundled-plugins: copying %s failed: %v", p.name, err)
			continue
		}
		log.Printf("bundled-plugins: installed %s -> %s", p.name, target)
	}

	// 2) register the loader rows in cordis.patch.yml if missing
	patchPath := filepath.Join(profileDir, "cordis.patch.yml")
	patch, err := os.ReadFile(patchPath)
	if err != nil {
		patch = []byte("# Your patch layer for this dsh profile, applied after every bundle layer:\n" +
			"# a top-level YAML array of loader patch entries (id-targeted config\n" +
			"# overrides, disables, and insert lists; " + "`!!js" + "`" + " expressions allowed).\n" +
			"[]\n")
	}
	for _, p := range bundledPlugins {
		if strings.Contains(string(patch), p.name) {
			continue // already registered
		}
		block := "\n# ── managed by dsh-local-plugins (bundled) ────────────────────────────\n" +
			"- insert:\n" +
			"    - id: " + p.id + "\n" +
			"      name: '" + p.name + "'\n"
		patch = []byte(strings.TrimRight(string(patch), "\n") + block)
		log.Printf("bundled-plugins: registered %s in %s", p.name, patchPath)
	}
	if err := os.WriteFile(patchPath, patch, 0o644); err != nil {
		log.Printf("bundled-plugins: writing %s failed: %v", patchPath, err)
		return
	}

	// 3) apply the ui-workspace menu patch (needed by dsh-session-delete)
	if bin != "" {
		patchUIWorkspace(dshInstallRoot(bin))
	}
}
