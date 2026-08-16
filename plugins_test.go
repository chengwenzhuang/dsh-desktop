package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeUIWorkspace returns a minimal ui-workspace client bundle containing the
// four patch anchors (identical indentation to the shipped bundle).
func fakeUIWorkspace() string {
	return strings.Join([]string{
		"\t\t\t\t{",
		"\t\t\t\t\tid: \"archive\",",
		"\t\t\t\t\tlabel: t(\"menu.archiveSession\"),",
		"\t\t\t\t\ticon: (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconArchiveOutline20, { size: 16 })",
		"\t\t\t\t}",
		"\t\t\t];",
		"\t\t\t\t\t\t\t\t\tif (id === \"archive\") onArchive(node.id);",
		"\t\t\t\t\t\t\t\t},",
		"\t\t\t\t\t\t\t\tportal: true,",
		"\t\t\t\"menu.fork\": \"分叉会话\",",
		"\t\t\t\"menu.archiveSession\": \"归档会话\",",
		"\t\t\t\"menu.fork\": \"Fork session\",",
		"\t\t\t\"menu.archiveSession\": \"Archive session\",",
	}, "\n")
}

func TestEnsureBundledPlugins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)

	install := filepath.Join(home, "dsh-install")
	bin := filepath.Join(install, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	wsDir := filepath.Join(install, "node_modules", "@deepseek-ai", "dsh-client-ui-workspace", "lib")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "client.js"), []byte(fakeUIWorkspace()), 0o644); err != nil {
		t.Fatal(err)
	}

	ensureBundledPlugins(bin)

	for _, name := range []string{"dsh-local-plugins", "dsh-session-delete", "dsh-updater"} {
		nm := filepath.Join(home, "profiles", "web", "node_modules", name)
		if !fileExists(filepath.Join(nm, "package.json")) {
			t.Fatalf("%s not copied", name)
		}
		if !fileExists(filepath.Join(nm, "lib", "index.js")) || !fileExists(filepath.Join(nm, "lib", "client.js")) {
			t.Fatalf("%s lib missing", name)
		}
	}

	patch, err := os.ReadFile(filepath.Join(home, "profiles", "web", "cordis.patch.yml"))
	if err != nil {
		t.Fatalf("patch missing: %v", err)
	}
	for _, name := range []string{"dsh-local-plugins", "dsh-session-delete"} {
		if !strings.Contains(string(patch), name) {
			t.Fatalf("row %s not registered", name)
		}
	}

	patched, err := os.ReadFile(filepath.Join(wsDir, "client.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"window.__DSH_SESSION_DELETE__ === true",
		"dsh:session-delete",
		"\"menu.deleteSession\": \"删除会话\"",
		"\"menu.deleteSession\": \"Delete session\"",
	} {
		if !strings.Contains(string(patched), needle) {
			t.Fatalf("ui-workspace patch missing %q", needle)
		}
	}
	// Patch #2 must keep the comma after the onSelect close — dropping it (as
	// older exes did) leaves invalid JS that fails to load with
	// "loaded without registering".
	commaClose := "\t\t\t\t\t\t\t\t\t}\n\t\t\t\t\t\t\t\t},\n\t\t\t\t\t\t\t\tportal: true,"
	if !strings.Contains(string(patched), commaClose) {
		t.Fatalf("ui-workspace patch dropped the comma after onSelect close:\n%s", patched)
	}

	firstPatch := string(patch)
	firstWS := string(patched)
	ensureBundledPlugins(bin)
	secondPatch, _ := os.ReadFile(filepath.Join(home, "profiles", "web", "cordis.patch.yml"))
	secondWS, _ := os.ReadFile(filepath.Join(wsDir, "client.js"))
	if string(secondPatch) != firstPatch {
		t.Fatalf("patch not idempotent")
	}
	if string(secondWS) != firstWS {
		t.Fatalf("ui-workspace patch not idempotent")
	}

	// 内嵌插件版本更新（.dsh-digest 摘要不匹配）时应重装插件
	updaterDir := filepath.Join(home, "profiles", "web", "node_modules", "dsh-updater")
	marker := filepath.Join(updaterDir, ".dsh-digest")
	if err := os.WriteFile(marker, []byte("stale-digest"), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureBundledPlugins(bin)
	refreshed, err := os.ReadFile(marker)
	if err != nil || string(refreshed) == "stale-digest" {
		t.Fatalf("plugin not reinstalled on digest mismatch")
	}
	clientData, err := os.ReadFile(filepath.Join(updaterDir, "lib", "client.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clientData), "dsh-updater") {
		t.Fatalf("reinstalled client.js looks wrong")
	}

	patch2 := "# header\n[]\n- insert:\n    - id: my-plugin\n      name: 'my-plugin'\n"
	if err := os.WriteFile(filepath.Join(home, "profiles", "web", "cordis.patch.yml"), []byte(patch2), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureBundledPlugins(bin)
	after, _ := os.ReadFile(filepath.Join(home, "profiles", "web", "cordis.patch.yml"))
	if !strings.Contains(string(after), "my-plugin") {
		t.Fatalf("existing rows clobbered")
	}
}

// TestPatchFileStaysLoadable guards against the `[]`-template trap: dsh seeds
// cordis.patch.yml with a lone `[]` document, and dsh-app-boot's parsePatchList
// (js-yaml) rejects a stream with a second root node ("end of the stream or a
// document separator is expected"), so any insert block appended after `[]`
// breaks boot with 服务启动失败. ensureBundledPlugins must never leave `[]`
// in a file that carries entries — on fresh installs and when healing files
// already broken by older exes.
func TestPatchFileStaysLoadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)

	install := filepath.Join(home, "dsh-install")
	bin := filepath.Join(install, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	profile := filepath.Join(home, "profiles", "web")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	patchPath := filepath.Join(profile, "cordis.patch.yml")

	// Fresh profile: the template + all bundled insert blocks must not keep `[]`.
	ensureBundledPlugins(bin)
	assertPatchLoadable(t, patchPath, true)

	// A file already broken by an older exe (`[]` next to entries) is healed on
	// the next run without clobbering the rows.
	broken := "# header\n[]\n- insert:\n    - id: my-plugin\n      name: 'my-plugin'\n"
	if err := os.WriteFile(patchPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureBundledPlugins(bin)
	after, _ := os.ReadFile(patchPath)
	if !strings.Contains(string(after), "my-plugin") {
		t.Fatalf("existing rows clobbered while healing")
	}
	assertPatchLoadable(t, patchPath, true)

	// A `[]`-only file (dsh's pristine empty patch layer) gets the blocks
	// appended and the `[]` marker dropped — the exact first-run sequence that
	// used to break boot.
	pristine := "# Your patch layer for this dsh profile, applied after every bundle layer:\n# a top-level YAML array of loader patch entries (id-targeted config\n# overrides, disables, and insert lists; `!!js` expressions allowed).\n[]\n"
	if err := os.WriteFile(patchPath, []byte(pristine), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureBundledPlugins(bin)
	assertPatchLoadable(t, patchPath, true)
	healed, _ := os.ReadFile(patchPath)
	if strings.Contains(string(healed), "\n[]\n") || strings.HasPrefix(string(healed), "[]") {
		t.Fatalf("empty-array marker survived next to entries:\n%s", healed)
	}
}

// TestPatchUIWorkspaceHealsCorruptedBundle guards against older exes' patch #2
// regression: they replaced the onSelect close "}," with "}" (dropping the
// comma), producing a syntax error that made the ui-workspace bundle fail to
// load ("client-modules: bundle ... loaded without registering"). The next run
// of ensureBundledPlugins must restore the comma.
func TestPatchUIWorkspaceHealsCorruptedBundle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)

	install := filepath.Join(home, "dsh-install")
	bin := filepath.Join(install, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	wsDir := filepath.Join(install, "node_modules", "@deepseek-ai", "dsh-client-ui-workspace", "lib")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate the deterministic corruption produced by older exes: the onSelect
	// close lost its comma (delete block already present, "portal:" next).
	corrupted := strings.Join([]string{
		"\t\t\t\t{",
		"\t\t\t\t\tid: \"archive\",",
		"\t\t\t\t\tlabel: t(\"menu.archiveSession\"),",
		"\t\t\t\t\ticon: (0, react_jsx_runtime.jsx)(_deepseek_ai_dsh_client_ui_primitives.IconArchiveOutline20, { size: 16 })",
		"\t\t\t\t}",
		"\t\t\t];",
		"\t\t\t\t\t\t\t\t\tif (id === \"archive\") onArchive(node.id);",
		"\t\t\t\t\t\t\t\t\tif (id === \"delete\") {",
		"\t\t\t\t\t\t\t\t\t\twindow.dispatchEvent(new CustomEvent(\"dsh:session-delete\", { detail: { sessionId: node.id, title: row.title } }));",
		"\t\t\t\t\t\t\t\t\t}",
		"\t\t\t\t\t\t\t\t}",
		"\t\t\t\t\t\t\t\tportal: true,",
		"\t\t\t\"menu.fork\": \"分叉会话\",",
		"\t\t\t\"menu.archiveSession\": \"归档会话\",",
		"\t\t\t\"menu.fork\": \"Fork session\",",
		"\t\t\t\"menu.archiveSession\": \"Archive session\",",
	}, "\n")
	clientPath := filepath.Join(wsDir, "client.js")
	if err := os.WriteFile(clientPath, []byte(corrupted), 0o644); err != nil {
		t.Fatal(err)
	}

	ensureBundledPlugins(bin)

	after, err := os.ReadFile(clientPath)
	if err != nil {
		t.Fatal(err)
	}
	healedClose := "\t\t\t\t\t\t\t\t\t}\n\t\t\t\t\t\t\t\t},\n\t\t\t\t\t\t\t\tportal: true,"
	if !strings.Contains(string(after), healedClose) {
		t.Fatalf("corrupted onSelect close not healed:\n%s", after)
	}
	if strings.Contains(string(after), "\t\t\t\t\t\t\t\t}\n\t\t\t\t\t\t\t\tportal: true,") {
		t.Fatalf("comma still missing after heal:\n%s", after)
	}

	// Idempotent: a second run must leave the healed bundle untouched.
	first := string(after)
	ensureBundledPlugins(bin)
	second, _ := os.ReadFile(clientPath)
	if string(second) != first {
		t.Fatalf("heal not idempotent")
	}
}

// assertPatchLoadable checks the invariants that keep cordis.patch.yml loadable
// as a top-level YAML array by dsh-app-boot: no `[]` line alongside real
// entries, and every content line belongs to a `- insert:` block list.
func assertPatchLoadable(t *testing.T, path string, expectEntries bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("patch missing: %v", err)
	}
	text := string(data)
	entries := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "[]" {
			t.Fatalf("lone [] marker next to real entries:\n%s", text)
		}
		entries++
	}
	if expectEntries && entries == 0 {
		t.Fatalf("patch file has no entries:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.TrimLeft(line, " \t") != line {
			continue // blank, comment, or indented row content
		}
		if !strings.HasPrefix(line, "- ") {
			t.Fatalf("unexpected top-level content line %q in:\n%s", line, text)
		}
	}
}
