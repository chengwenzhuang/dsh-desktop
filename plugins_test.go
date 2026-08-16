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
