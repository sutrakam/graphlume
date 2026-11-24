package indexer

import (
	"os"
	"strings"
	"testing"
)

func TestFallbackRetriesScipPythonWithoutPackageDiscovery(t *testing.T) {
	cmd := Command{Args: []string{"index", "--output", "x.scip"}}
	failed := Result{ExitCode: 255}

	work := t.TempDir() + "/p"
	env := work + ".environment.json"
	retry, ok := fallbackCommand(Installed{Indexer: SCIPPython}, RunnerNative, cmd, failed, nil, work, "")
	if !ok || !hasArg(retry.Args, "--environment") || retry.Args[len(retry.Args)-1] != env {
		t.Fatalf("a failed scip-python run is retried with --environment: %v %v", ok, retry.Args)
	}
	if hasArg(cmd.Args, "--environment") {
		t.Fatal("the original command's arguments must not be modified")
	}
	for name, c := range map[string]struct {
		inst Installed
		kind string
		cmd  Command
		res  Result
	}{
		"succeeded":                     {Installed{Indexer: SCIPPython}, RunnerNative, cmd, Result{}},
		"timed out":                     {Installed{Indexer: SCIPPython}, RunnerNative, cmd, Result{ExitCode: 1, TimedOut: true}},
		"already set":                   {Installed{Indexer: SCIPPython}, RunnerNative, Command{Args: []string{"index", "--environment", "[]"}}, failed},
		"docker":                        {Installed{Indexer: SCIPPython}, RunnerDocker, cmd, failed},
		"typescript, not out of memory": {Installed{Indexer: SCIPTypeScript}, RunnerNative, cmd, failed},
		"other indexers":                {Installed{Indexer: SCIPGo}, RunnerNative, cmd, failed},
	} {
		if _, ok := fallbackCommand(c.inst, c.kind, c.cmd, c.res, nil, t.TempDir()+"/p", ""); ok {
			t.Errorf("%s: no retry expected", name)
		}
	}
}

func TestFallbackRaisesTheNodeHeapOnce(t *testing.T) {
	cmd := Command{Args: []string{"index"}, Env: []string{"NODE_OPTIONS=--max-old-space-size=4096"}}
	retry, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, cmd, Result{ExitCode: 134}, nil, "", "")
	if !ok || nodeHeapMB(retry.Env) != largeNodeHeapMB {
		t.Fatalf("an out-of-memory run is retried with a larger heap: %v %v", ok, retry.Env)
	}
	if r2, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, cmd, Result{ExitCode: -1}, nil, "", ""); !ok || nodeHeapMB(r2.Env) != largeNodeHeapMB {
		t.Fatal("a Node process killed by a signal is treated as out of memory")
	}
	if _, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, retry, Result{ExitCode: 134}, nil, "", ""); ok {
		t.Fatal("a run already at the large heap is not retried again")
	}
}

func TestFallbackIndexesATypeScriptProjectWhoseTsconfigIsGenerated(t *testing.T) {
	project := t.TempDir()
	work := t.TempDir() + "/web"
	cmd := Command{Dir: project, Args: []string{"index", "--output", "/out/web.scip"}}
	failed := Result{ExitCode: 1, Stderr: []byte("- " + project + "/.nuxt/tsconfig.json (missing tsconfig.json)")}
	retry, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, cmd, failed, nil, work, "")
	if !ok || argAfter(retry.Args, "--cwd") != project || argAfter(retry.Args, "--output") != "/out/web.scip" || retry.Args[len(retry.Args)-1] != work+"-tsconfig" {
		t.Fatalf("retry args: %v", retry.Args)
	}
	if _, err := os.Stat(work + "-tsconfig/tsconfig.json"); err != nil {
		t.Fatalf("stand-in tsconfig not written: %v", err)
	}
	if _, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, retry, failed, nil, work, ""); ok {
		t.Fatal("the stand-in is tried once")
	}
}

func TestFailureHintNamesGeneratedTsconfigFixes(t *testing.T) {
	if h := failureHint(SCIPTypeScript, "- /repo/web/.nuxt/tsconfig.json (missing tsconfig.json)"); !strings.Contains(h, "nuxi prepare") {
		t.Errorf("Nuxt hint missing: %q", h)
	}
	if h := failureHint(SCIPPython, "anything"); h != "" {
		t.Errorf("unexpected hint: %q", h)
	}
}

func TestFallbackCoversAnUninstalledWorkspaceTsconfigAndADanglingLink(t *testing.T) {
	for name, stderr := range map[string]string{
		"extends a workspace package that is not installed": "error TS6053: File '@acme/tsconfig/base.json' not found.\n\nerror: no files got indexed.",
		"extends a tsconfig a framework generates":          "error TS5083: Cannot read file '/p/.nitro/types/tsconfig.json'.\n\nerror: no files got indexed.",
		"inferred walk stopped on a dangling link":          "node:fs:1740\nError: ENOENT: no such file or directory, stat '/r/.agents/skills/x'\n    at visitPath (/n/dist/src/inferTsconfig.js:57:17)",
	} {
		project := t.TempDir()
		work := t.TempDir() + "/web"
		cmd := Command{Dir: project, Args: []string{"index", "--output", "/out/web.scip", "--infer-tsconfig"}}
		retry, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, cmd, Result{ExitCode: 1, Stderr: []byte(stderr)}, nil, work, "")
		if !ok || argAfter(retry.Args, "--cwd") != project || hasArg(retry.Args, "--infer-tsconfig") {
			t.Errorf("%s: no stand-in retry: %v %v", name, ok, retry.Args)
		}
	}
	// A failure that is about the code, not the tsconfig, is not retried.
	cmd := Command{Dir: t.TempDir(), Args: []string{"index"}}
	if _, ok := fallbackCommand(Installed{Indexer: SCIPTypeScript}, RunnerNative, cmd, Result{ExitCode: 1, Stderr: []byte("error TS2304: Cannot find name 'x'.")}, nil, t.TempDir()+"/w", ""); ok {
		t.Error("an ordinary compile error must not trigger the tsconfig stand-in")
	}
}

func TestFailureHintNamesAnUninstalledWorkspace(t *testing.T) {
	h := failureHint(SCIPTypeScript, "error TS6053: File '@acme/tsconfig/base.json' not found.")
	if !strings.Contains(h, "install") {
		t.Errorf("hint does not say to install: %q", h)
	}
	if h := failureHint(SCIPTypeScript, "ENOENT stat\n at visitPath (inferTsconfig.js:57:17)"); !strings.Contains(h, "symbolic link") {
		t.Errorf("dangling-link hint missing: %q", h)
	}
	if h := failureHint(SCIPRuby, "error: Failed to find .gemspec file"); !strings.Contains(h, "gemspec") {
		t.Errorf("Ruby gemspec hint missing: %q", h)
	}
	if h := failureHint(SCIPRuby, "Bundler::GemNotFound"); !strings.Contains(h, "bundle install") {
		t.Errorf("Ruby bundle hint missing: %q", h)
	}
	if h := failureHint(SCIPJava, "error: release version 25 not supported"); !strings.Contains(h, "JDK") {
		t.Errorf("Java JDK version hint missing: %q", h)
	}
	if h := failureHint(SCIPJava, "sh: gradlew: not found"); !strings.Contains(h, "wrapper") {
		t.Errorf("Gradle wrapper hint missing: %q", h)
	}
	if h := failureHint(SCIPJava, "Could not resolve dependencies for project"); !strings.Contains(h, "dependencies") {
		t.Errorf("Java offline dependency hint missing: %q", h)
	}
}
