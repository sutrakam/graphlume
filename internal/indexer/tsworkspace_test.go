package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTree writes files (slash-separated paths under root) and returns root.
func writeFiles(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// t3Like is a small pnpm workspace laid out like the ones that were measured.
func t3Like(t *testing.T) string {
	t.Helper()
	return writeFiles(t, t.TempDir(), map[string]string{
		"package.json":                    `{"name":"root","private":true}`,
		"pnpm-workspace.yaml":             "packages:\n  - apps/*\n  - packages/*\n  - tooling/*\n\ncatalog:\n  zod: ^4\n",
		"packages/ui/package.json":        `{"name":"@acme/ui","exports":{".":"./src/index.ts","./button":"./src/button.tsx","./theme":{"types":"./dist/theme.d.ts","default":"./dist/theme.js"}}}`,
		"packages/ui/src/index.ts":        "export * from './button'\n",
		"packages/ui/src/button.tsx":      "export const Button = () => null\n",
		"packages/ui/src/theme.ts":        "export const theme = 1\n",
		"packages/ui/tsconfig.json":       `{"extends":"@acme/tsconfig/base.json","include":["src"]}`,
		"packages/db/package.json":        `{"name":"@acme/db","main":"./dist/index.js","types":"./dist/index.d.ts"}`,
		"packages/db/src/index.ts":        "export const db = 1\n",
		"packages/lib/package.json":       `{"name":"@acme/lib"}`,
		"packages/lib/index.ts":           "export const x = 1\n",
		"packages/noname/package.json":    `{"private":true}`,
		"tooling/typescript/package.json": `{"name":"@acme/tsconfig"}`,
		"tooling/typescript/base.json": `{
  // shared options
  "compilerOptions": {
    "paths": {"@shared/*": ["./shared/*"], "@ui-alias": ["../../packages/ui/src/index.ts"],},
    /* block */ "strict": true,
  },
}`,
		"apps/web/package.json":  `{"name":"@acme/web"}`,
		"apps/web/src/main.ts":   "import { Button } from '@acme/ui'\n",
		"apps/web/tsconfig.json": `{"extends":"@acme/tsconfig/base.json","compilerOptions":{"baseUrl":"src","paths":{"@/*":["./*"]}}}`,
	})
}

func TestWorkspacePatternsReadsPnpmAndPackageJSON(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"pnpm-workspace.yaml": "packages:\n  - 'apps/*'\n  - \"!apps/legacy\"\n",
	})
	got, ok := workspacePatterns(dir)
	if !ok || !reflect.DeepEqual(got, []string{"apps/*", "!apps/legacy"}) {
		t.Fatalf("pnpm: %v %v", got, ok)
	}
	arr := writeFiles(t, t.TempDir(), map[string]string{"package.json": `{"workspaces":["packages/*","tools/a"]}`})
	if got, ok := workspacePatterns(arr); !ok || !reflect.DeepEqual(got, []string{"packages/*", "tools/a"}) {
		t.Fatalf("array: %v %v", got, ok)
	}
	obj := writeFiles(t, t.TempDir(), map[string]string{"package.json": `{"workspaces":{"packages":["libs/*"],"nohoist":["x"]}}`})
	if got, ok := workspacePatterns(obj); !ok || !reflect.DeepEqual(got, []string{"libs/*"}) {
		t.Fatalf("object: %v %v", got, ok)
	}
	none := writeFiles(t, t.TempDir(), map[string]string{"package.json": `{"name":"x"}`})
	if _, ok := workspacePatterns(none); ok {
		t.Fatal("a package.json without workspaces declares none")
	}
	broken := writeFiles(t, t.TempDir(), map[string]string{"pnpm-workspace.yaml": "packages: [unclosed", "package.json": "{"})
	if _, ok := workspacePatterns(broken); ok {
		t.Fatal("unreadable manifests declare none")
	}
}

func TestExpandWorkspaceDirs(t *testing.T) {
	root := writeFiles(t, t.TempDir(), map[string]string{
		"apps/web/package.json":                "{}",
		"apps/legacy/package.json":             "{}",
		"packages/a/package.json":              "{}",
		"packages/a/nested/b/package.json":     "{}",
		"packages/@scope/c/package.json":       "{}",
		"packages/node_modules/x/package.json": "{}",
		"packages/.hidden/package.json":        "{}",
		"docs/package.json":                    "{}",
	})
	got := expandWorkspaceDirs(root, []string{"apps/*", "!apps/legacy", "packages/*", "packages/@scope/*", "docs"})
	want := []string{"apps/web", "docs", "packages/@scope", "packages/@scope/c", "packages/a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	deep := expandWorkspaceDirs(root, []string{"packages/**"})
	for _, d := range deep {
		if strings.Contains(d, "node_modules") || strings.Contains(d, ".hidden") {
			t.Errorf("a pattern entered %s", d)
		}
	}
	found := false
	for _, d := range deep {
		if d == "packages/a/nested/b" {
			found = true
		}
	}
	if !found {
		t.Errorf("** reaches nested directories: %v", deep)
	}
	if escaped := expandWorkspaceDirs(root, []string{"../*", "/etc", "."}); len(escaped) != 0 {
		t.Errorf("a pattern left the workspace: %v", escaped)
	}
}

func TestWorkspaceMembersPickSourceEntries(t *testing.T) {
	root := t3Like(t)
	patterns, _ := workspacePatterns(root)
	members := workspaceMembers(root, root, patterns)
	byName := map[string]workspacePackage{}
	for _, m := range members {
		byName[m.Name] = m
	}
	if _, ok := byName[""]; ok || len(byName) != 5 {
		t.Fatalf("members (a package without a name is not one): %v", members)
	}
	rel := func(p string) string { r, _ := filepath.Rel(root, p); return filepath.ToSlash(r) }

	if got := rel(byName["@acme/ui"].Entry); got != "packages/ui/src/index.ts" {
		t.Errorf("exports \".\": %s", got)
	}
	if got := rel(byName["@acme/ui"].Subpaths["/button"]); got != "packages/ui/src/button.tsx" {
		t.Errorf("exports subpath: %s", got)
	}
	if got := rel(byName["@acme/ui"].Subpaths["/theme"]); got != "packages/ui/src/theme.ts" {
		t.Errorf("a built subpath is read from the source tree: %s", got)
	}
	if got := rel(byName["@acme/db"].Entry); got != "packages/db/src/index.ts" {
		t.Errorf("main and types under dist map to src: %s", got)
	}
	if got := rel(byName["@acme/lib"].Entry); got != "packages/lib/index.ts" {
		t.Errorf("fallback to index.ts: %s", got)
	}
}

func TestWorkspaceEntryConditionsAndFallbacks(t *testing.T) {
	root := writeFiles(t, t.TempDir(), map[string]string{
		"package.json":         "{}",
		"p/a/package.json":     `{"name":"a","exports":{"types":"./types/index.d.ts","import":"./esm/index.js","default":"./cjs/index.js"}}`,
		"p/a/src/index.ts":     "export {}\n",
		"p/b/package.json":     `{"name":"b","exports":{".":{"import":{"types":"./dist/x.d.mts","default":"./dist/x.mjs"},"require":"./dist/x.cjs"}}}`,
		"p/b/src/x.ts":         "export {}\n",
		"p/c/package.json":     `{"name":"c","main":"./src"}`,
		"p/c/src/index.tsx":    "export {}\n",
		"p/d/package.json":     `{"name":"d","main":"./index.js"}`,
		"p/d/index.js":         "module.exports = {}\n",
		"p/e/package.json":     `{"name":"e","exports":{"./*":"./src/*.ts","./feature/*":"./dist/feature/*.js"}}`,
		"p/e/src/index.ts":     "export {}\n",
		"p/e/src/feature/z.ts": "export {}\n",
		"p/f/package.json":     `{"name":"f","main":"../../outside.js"}`,
		"outside.js":           "x",
		"p/g/package.json":     `{"name":"g","main":"dist/index.js"}`,
		"p/g/dist/index.js":    "module.exports = {}\n",
		"p/h/package.json":     `{"name":"bad name"}`,
	})
	members := workspaceMembers(root, root, []string{"p/*"})
	by := map[string]workspacePackage{}
	for _, m := range members {
		by[m.Name] = m
	}
	rel := func(p string) string { r, _ := filepath.Rel(root, p); return filepath.ToSlash(r) }
	for name, want := range map[string]string{
		"a": "p/a/src/index.ts",  // types/esm/cjs are all build output; src/index.ts is the fallback
		"b": "p/b/src/x.ts",      // nested conditions, .d.mts and .mjs map to the source file
		"c": "p/c/src/index.tsx", // a directory entry resolves to its index file
		"d": "p/d/index.js",      // plain JavaScript is used as it is
		"e": "p/e/src/index.ts",  // no "." export: the src/index.ts fallback
		"g": "p/g/dist/index.js", // no source tree: the built file is the only one
	} {
		if got := rel(by[name].Entry); got != want {
			t.Errorf("%s: entry %q want %q", name, got, want)
		}
	}
	if by["f"].Entry != "" && !isWithin(root, by["f"].Entry) {
		t.Errorf("an entry outside the repository: %s", by["f"].Entry)
	}
	if _, ok := by["bad name"]; ok {
		t.Error("a name with a space is not a usable path key")
	}
	if got := rel(by["e"].Subpaths["/*"]); got != "p/e/src/*.ts" {
		t.Errorf("wildcard export: %q", got)
	}
	if got := rel(by["e"].Subpaths["/feature/*"]); got != "p/e/dist/feature/*.js" && got != "p/e/src/feature/*.ts" {
		t.Errorf("built wildcard export: %q", got)
	}
}

func TestWorkspacePathsMapNameAndFolder(t *testing.T) {
	root := t3Like(t)
	patterns, _ := workspacePatterns(root)
	paths := workspacePaths(workspaceMembers(root, root, patterns))
	ui := filepath.Join(root, "packages", "ui")
	if got := paths["@acme/ui"]; !reflect.DeepEqual(got, []string{filepath.Join(ui, "src", "index.ts")}) {
		t.Errorf("name: %v", got)
	}
	if got := paths["@acme/ui/button"]; !reflect.DeepEqual(got, []string{filepath.Join(ui, "src", "button.tsx")}) {
		t.Errorf("exported subpath: %v", got)
	}
	if got := paths["@acme/ui/*"]; !reflect.DeepEqual(got, []string{filepath.Join(ui, "*"), filepath.Join(ui, "src", "*")}) {
		t.Errorf("name/*: %v", got)
	}
	for _, targets := range paths {
		for _, p := range targets {
			if !filepath.IsAbs(p) || !isWithin(root, p) {
				t.Errorf("target %s is not an absolute path inside the repository", p)
			}
		}
	}
}

func TestStripJSONC(t *testing.T) {
	in := "\xef\xbb\xbf{\n // line\n \"a\": \"http://x // not a comment\", /* c */ \"b\": [1, 2,],\n \"c\": {\"d\": \"e,\"},\n}\n"
	var v map[string]any
	if err := json.Unmarshal(stripJSONC([]byte(in)), &v); err != nil {
		t.Fatalf("not strict JSON: %v\n%s", err, stripJSONC([]byte(in)))
	}
	if v["a"] != "http://x // not a comment" {
		t.Errorf("a string was cut: %v", v["a"])
	}
	if c := v["c"].(map[string]any); c["d"] != "e," {
		t.Errorf("a comma inside a string was removed: %v", c)
	}
}

func TestReadTsconfigPathsFollowsExtendsThroughMembers(t *testing.T) {
	root := t3Like(t)
	patterns, _ := workspacePatterns(root)
	members := workspaceMembers(root, root, patterns)
	web := filepath.Join(root, "apps", "web")
	got := readTsconfigPaths(root, web, members)
	if got.BaseURL != filepath.Join(web, "src") {
		t.Errorf("baseUrl %q", got.BaseURL)
	}
	// "@/*" is the project's own, relative to its baseUrl; the others come from
	// the base config of the member @acme/tsconfig, resolved against the
	// project's baseUrl as the compiler does.
	if got.Paths["@/*"][0] != filepath.Join(web, "src", "*") {
		t.Errorf("own alias: %v", got.Paths["@/*"])
	}
	if got.Paths["@shared/*"][0] != filepath.Join(web, "src", "shared", "*") {
		t.Errorf("inherited alias: %v", got.Paths["@shared/*"])
	}

	// Without a baseUrl, targets are relative to the config that defines them.
	ui := filepath.Join(root, "packages", "ui")
	got = readTsconfigPaths(root, ui, members)
	if got.BaseURL != "" {
		t.Errorf("no baseUrl expected, got %q", got.BaseURL)
	}
	if want := filepath.Join(root, "tooling", "typescript", "shared", "*"); got.Paths["@shared/*"][0] != want {
		t.Errorf("alias of the base config: %v want %s", got.Paths["@shared/*"], want)
	}
	if want := filepath.Join(root, "packages", "ui", "src", "index.ts"); got.Paths["@ui-alias"][0] != want {
		t.Errorf("relative target: %v want %s", got.Paths["@ui-alias"], want)
	}

	// A package that is not a member is not read, and an escaping target is dropped.
	esc := writeFiles(t, t.TempDir(), map[string]string{
		"a/tsconfig.json": `{"extends":"some-published-config/base.json","compilerOptions":{"paths":{"x":["../../../etc/passwd"]}}}`,
	})
	if p := readTsconfigPaths(esc, filepath.Join(esc, "a"), nil); len(p.Paths) != 0 {
		t.Errorf("targets outside the repository must be dropped: %v", p.Paths)
	}
}

func TestExtendsCycleTerminates(t *testing.T) {
	root := writeFiles(t, t.TempDir(), map[string]string{
		"a/tsconfig.json": `{"extends":"./b.json"}`,
		"a/b.json":        `{"extends":"./tsconfig.json","compilerOptions":{"paths":{"k":["./k"]}}}`,
	})
	if p := readTsconfigPaths(root, filepath.Join(root, "a"), nil); len(p.Paths["k"]) != 1 {
		t.Errorf("paths: %v", p.Paths)
	}
}

// readStandIn returns the paths and baseUrl of the stand-in tsconfig in dir.
func readStandIn(t *testing.T, dir string) (map[string][]string, string, map[string]any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "tsconfig.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		CompilerOptions struct {
			Paths   map[string][]string `json:"paths"`
			BaseURL string              `json:"baseUrl"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	return cfg.CompilerOptions.Paths, cfg.CompilerOptions.BaseURL, raw
}

func TestStandInTsconfigMapsSiblingsAndKeepsProjectAliases(t *testing.T) {
	root := t3Like(t)
	web := filepath.Join(root, "apps", "web")
	dir := filepath.Join(t.TempDir(), "web-tsconfig")
	if err := writeStandInTsconfig(dir, web, root); err != nil {
		t.Fatal(err)
	}
	paths, baseURL, _ := readStandIn(t, dir)
	if baseURL != filepath.Join(web, "src") {
		t.Errorf("the project's own baseUrl is kept: %q", baseURL)
	}
	if got := paths["@acme/ui"]; len(got) != 1 || !strings.HasSuffix(filepath.ToSlash(got[0]), "packages/ui/src/index.ts") {
		t.Errorf("sibling package: %v", got)
	}
	if got := paths["@acme/ui/button"]; len(got) != 1 || !strings.HasSuffix(filepath.ToSlash(got[0]), "packages/ui/src/button.tsx") {
		t.Errorf("sibling subpath: %v", got)
	}
	if got := paths["@/*"]; len(got) != 1 || got[0] != filepath.Join(web, "src", "*") {
		t.Errorf("the project's alias beats the stand-in's own: %v", got)
	}
	if got := paths["~/*"]; len(got) != 1 || got[0] != filepath.Join(web, "*") {
		t.Errorf("the stand-in's aliases stay: %v", got)
	}
	if _, ok := paths["@shared/*"]; !ok {
		t.Error("an alias the workspace's base config defines is merged")
	}
}

func TestStandInTsconfigOutsideAWorkspaceKeepsOnlyItsAliases(t *testing.T) {
	proj := writeFiles(t, t.TempDir(), map[string]string{"package.json": `{"name":"solo"}`, "src/a.ts": "export {}\n"})
	dir := filepath.Join(t.TempDir(), "solo-tsconfig")
	if err := writeStandInTsconfig(dir, proj, proj); err != nil {
		t.Fatal(err)
	}
	paths, baseURL, _ := readStandIn(t, dir)
	if baseURL != proj || len(paths) != 4 {
		t.Errorf("baseUrl %q paths %v", baseURL, paths)
	}
}

func TestWorkspaceStandInAppliesToAnUninstalledRootOnly(t *testing.T) {
	root := t3Like(t)
	out := t.TempDir()
	dir, ok := workspaceStandIn(root, root, filepath.Join(out, "typescript-workspace"))
	if !ok || !isWorkspaceStandIn([]string{"index", "--cwd", root, dir}) {
		t.Fatalf("an uninstalled workspace root is indexed through the stand-in: %q %v", dir, ok)
	}
	if paths, _, _ := readStandIn(t, dir); paths["@acme/db"] == nil {
		t.Error("members are mapped")
	}
	if _, ok := workspaceStandIn(filepath.Join(root, "apps", "web"), root, filepath.Join(out, "x-workspace")); ok {
		t.Error("a member is not a workspace root")
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, ok := workspaceStandIn(root, root, filepath.Join(out, "y-workspace")); ok {
		t.Error("an installed workspace resolves its members through node_modules")
	}
	empty := writeFiles(t, t.TempDir(), map[string]string{"package.json": `{"workspaces":["packages/*"]}`})
	if _, ok := workspaceStandIn(empty, empty, filepath.Join(out, "z-workspace")); ok {
		t.Error("a workspace without members has nothing to map")
	}
}

func TestCommandUsesTheWorkspaceStandInForARootWithoutATsconfig(t *testing.T) {
	root := t3Like(t)
	out := t.TempDir()
	e := &Executor{RunnerKind: RunnerNative}
	p := Project{Language: LangTypeScript, Dir: ".", Markers: []string{"package.json"}}
	cmd, err := e.command(Installed{Indexer: SCIPTypeScript, Path: "/bin/scip-typescript"}, p, "", root, root, out, filepath.Join(out, "typescript.scip"))
	if err != nil {
		t.Fatal(err)
	}
	if hasArg(cmd.Args, "--infer-tsconfig") || argAfter(cmd.Args, "--cwd") != root || !isWorkspaceStandIn(cmd.Args) {
		t.Fatalf("args: %v", cmd.Args)
	}
	// A root with its own tsconfig keeps it.
	p.Markers = []string{"package.json", "tsconfig.json"}
	cmd, _ = e.command(Installed{Indexer: SCIPTypeScript, Path: "/bin/scip-typescript"}, p, "", root, root, out, filepath.Join(out, "typescript.scip"))
	if hasArg(cmd.Args, "--cwd") {
		t.Fatalf("a root with a tsconfig runs on it: %v", cmd.Args)
	}
	// A project that is not a workspace root is inferred as before.
	solo := writeFiles(t, t.TempDir(), map[string]string{"package.json": `{}`})
	p = Project{Language: LangTypeScript, Dir: ".", Markers: []string{"package.json"}}
	cmd, _ = e.command(Installed{Indexer: SCIPTypeScript, Path: "/bin/scip-typescript"}, p, "", solo, solo, out, filepath.Join(out, "typescript.scip"))
	if !hasArg(cmd.Args, "--infer-tsconfig") {
		t.Fatalf("a plain project is inferred: %v", cmd.Args)
	}
}

func TestWorkspaceStandInFallsBackToTheInferredTsconfigOnce(t *testing.T) {
	root := t3Like(t)
	out := t.TempDir()
	dir, ok := workspaceStandIn(root, root, filepath.Join(out, "typescript-workspace"))
	if !ok {
		t.Fatal("no stand-in")
	}
	cmd := Command{Dir: root, Args: []string{"index", "--cwd", root, "--output", "/o/ts.scip", dir}}
	retry, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, cmd, Result{ExitCode: 1, Stderr: []byte("error: no files got indexed")}, nil, filepath.Join(out, "typescript"), root)
	if !ok || !hasArg(retry.Args, "--infer-tsconfig") || hasArg(retry.Args, "--cwd") || argAfter(retry.Args, "--output") != "/o/ts.scip" {
		t.Fatalf("retry: %v %v", ok, retry.Args)
	}
	if _, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, retry, Result{ExitCode: 1, Stderr: []byte("error: no files got indexed")}, nil, filepath.Join(out, "typescript"), root); ok {
		t.Fatal("the inferred tsconfig is the last resort")
	}
	// Out of memory is the heap's problem, not the tsconfig's: the heap is raised first.
	oom, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, cmd, Result{ExitCode: 134}, nil, filepath.Join(out, "typescript"), root)
	if !ok || !hasArg(oom.Args, "--cwd") || nodeHeapMB(oom.Env) != largeNodeHeapMB {
		t.Fatalf("an out-of-memory stand-in run is retried with a larger heap: %v %v", ok, oom)
	}
}

func TestStandInAfterAWorkspaceTsconfigFailureMapsTheSiblings(t *testing.T) {
	root := t3Like(t)
	ui := filepath.Join(root, "packages", "ui")
	work := filepath.Join(t.TempDir(), "typescript-packages_ui")
	cmd := Command{Dir: ui, Args: []string{"index", "--output", "/o/ui.scip"}}
	failed := Result{ExitCode: 1, Stderr: []byte("error TS6053: File '@acme/tsconfig/base.json' not found.")}
	retry, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, cmd, failed, nil, work, root)
	if !ok {
		t.Fatal("no retry")
	}
	paths, _, _ := readStandIn(t, retry.Args[len(retry.Args)-1])
	if len(paths["@acme/db"]) != 1 || len(paths["@acme/ui/*"]) != 2 {
		t.Errorf("the retry does not map the workspace: %v", paths)
	}
}
