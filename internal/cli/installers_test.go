package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoFile reads a file from the repository root.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// releaseArchive is one archive the release workflow builds.
type releaseArchive struct {
	name    string   // as written in the workflow, with ${VERSION} in it
	members []string // the paths packed, in order
}

// workflowArchives reads the archive layout out of the release workflow: the
// name of each versioned archive and the paths it packs. The workflow builds
// them with two command shapes, and both list their members explicitly:
//
//	tar -czf "dist/NAME.tar.gz" -C dist/DIR member member ...
//	(cd dist/DIR && zip -qr "../NAME.zip" member member ...)
func workflowArchives(t *testing.T) []releaseArchive {
	t.Helper()
	wf := repoFile(t, ".github/workflows/release.yml")
	var out []releaseArchive
	tarRE := regexp.MustCompile(`tar -czf "dist/(graphlume_\$\{VERSION\}_[a-z0-9_]+\.tar\.gz)" -C dist/\S+ (.+)`)
	for _, m := range tarRE.FindAllStringSubmatch(wf, -1) {
		// The _cgo archives are optional extras (the cgo tree-sitter runtime)
		// that no installer asks for; the installers' targets are the
		// pure-Go archives, and those are what this test pins. Their layout
		// is still checked below.
		if strings.HasSuffix(m[1], "_cgo.tar.gz") {
			if got := strings.Fields(m[2]); len(got) == 0 || got[0] != "graphlume" {
				t.Errorf("%s packs %v; the binary must be the first member, at the root", m[1], got)
			}
			continue
		}
		out = append(out, releaseArchive{name: m[1], members: strings.Fields(m[2])})
	}
	zipRE := regexp.MustCompile(`zip -qr "\.\./(graphlume_\$\{VERSION\}_[a-z0-9_]+\.zip)" ([^)]+)\)`)
	for _, m := range zipRE.FindAllStringSubmatch(wf, -1) {
		out = append(out, releaseArchive{name: m[1], members: strings.Fields(m[2])})
	}
	if len(out) != 5 {
		t.Fatalf("read %d versioned archives out of release.yml, want the 5 release targets: %+v\n"+
			"If the workflow now builds archives another way, teach this test the new shape; "+
			"the installers depend on the layout it is checking.", len(out), out)
	}
	return out
}

// install.ps1 copied $tmp\graphlume_<tag>_windows_amd64\graphlume.exe, a
// path no release archive has ever contained: the workflow zips the files at
// the archive root. Every Windows install failed at Copy-Item. Nothing
// compared the script with the workflow, so nothing noticed.
//
// This reads both and compares them: the archive each installer asks for is
// one the workflow builds, and the path each installer takes the binary from
// is where the workflow puts it.
func TestInstallersMatchTheReleaseWorkflow(t *testing.T) {
	archives := workflowArchives(t)
	byName := map[string]releaseArchive{}
	for _, a := range archives {
		byName[a.name] = a
		// The binary is the first member and sits at the root of the archive.
		want := "graphlume"
		if strings.Contains(a.name, "windows") {
			want = "graphlume.exe"
		}
		if len(a.members) == 0 || a.members[0] != want {
			t.Errorf("%s packs %v; the installers expect %s at the archive root", a.name, a.members, want)
		}
		for _, m := range a.members {
			if strings.ContainsAny(m, `/\`) {
				t.Errorf("%s packs %q inside a directory; the installers read the archive root", a.name, m)
			}
		}
	}

	// install.sh builds the archive name from uname, for these combinations.
	sh := repoFile(t, "install.sh")
	if !strings.Contains(sh, `archive="graphlume_${VERSION}_${os}_${arch}.tar.gz"`) {
		t.Fatal("install.sh no longer names its archive graphlume_${VERSION}_${os}_${arch}.tar.gz; update this test")
	}
	for _, target := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
		name := "graphlume_${VERSION}_" + target + ".tar.gz"
		if _, ok := byName[name]; !ok {
			t.Errorf("install.sh can ask for %s, which release.yml does not build", name)
		}
	}
	if !strings.Contains(sh, `extracted="${tmp}/graphlume"`) {
		t.Error(`install.sh does not take the binary from "${tmp}/graphlume", the archive root`)
	}

	ps := repoFile(t, "install.ps1")
	if !strings.Contains(ps, `$archive = "graphlume_${Version}_windows_amd64.zip"`) {
		t.Fatal("install.ps1 no longer names its archive graphlume_${Version}_windows_amd64.zip; update this test")
	}
	if _, ok := byName["graphlume_${VERSION}_windows_amd64.zip"]; !ok {
		t.Error("install.ps1 asks for graphlume_${VERSION}_windows_amd64.zip, which release.yml does not build")
	}
	if !strings.Contains(ps, `$src = Join-Path $tmp "graphlume.exe"`) {
		t.Error(`install.ps1 does not take the binary from (Join-Path $tmp "graphlume.exe"), the archive root`)
	}
	// Any path into a subdirectory of the extracted archive is a path to
	// nothing.
	if m := regexp.MustCompile(`Join-Path \$tmp "[^"]*[\\/][^"]*"`).FindString(ps); m != "" {
		t.Errorf("install.ps1 reads a subdirectory of the archive, which has none: %s", m)
	}

	// Both verify against SHA256SUMS, which the workflow writes with bare
	// file names.
	wf := repoFile(t, ".github/workflows/release.yml")
	if !strings.Contains(wf, "shasum -a 256 *.tar.gz *.zip *.exe > SHA256SUMS") {
		t.Error("release.yml no longer writes SHA256SUMS with bare archive names; both installers look names up in it")
	}
}

// An installer line that links, copies or moves a file onto itself is a
// defect: in install.sh, `ln -sf X X` replaces the installed binary with a
// symlink to itself.
func TestInstallersDoNotOperateOnOneFileTwice(t *testing.T) {
	sh := repoFile(t, "install.sh")
	for _, line := range strings.Split(sh, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		for i := 0; i+2 < len(f); i++ {
			if (f[i] == "ln" || f[i] == "cp" || f[i] == "mv") && strings.HasPrefix(f[i+1], "-") &&
				f[i+2] != "" && i+3 < len(f) && f[i+2] == f[i+3] {
				t.Errorf("install.sh: %s with the same source and destination: %s", f[i], strings.TrimSpace(line))
			}
		}
	}
	for name, text := range map[string]string{"install.sh": sh, "install.ps1": repoFile(t, "install.ps1")} {
		if strings.Contains(text, "alias") {
			t.Errorf("%s still mentions an alias; there is one command name", name)
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(text, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "Copy-Item") || strings.HasPrefix(l, "xattr -d") {
				if seen[l] {
					t.Errorf("%s repeats a line: %s", name, l)
				}
				seen[l] = true
			}
		}
	}
}

// Windows PowerShell 5.1 reads a script with no byte-order mark as the ANSI
// code page. The bytes of a UTF-8 dash include 0x94, which that code page
// reads as a closing smart quote, and PowerShell accepts a smart quote as a
// string delimiter: a dash inside a string ends the string and the script does
// not parse. The file is kept ASCII so it means the same in every encoding.
func TestInstallPS1IsASCII(t *testing.T) {
	for i, line := range strings.Split(repoFile(t, "install.ps1"), "\n") {
		for _, r := range line {
			if r > 127 {
				t.Errorf("install.ps1:%d has a non-ASCII character %q: %s", i+1, r, line)
				break
			}
		}
	}
}

// Parses install.ps1 with PowerShell where one is installed, which is every
// Windows CI runner and few other places. Skipped elsewhere: the structural
// tests above do not need it.
func TestInstallPS1Parses(t *testing.T) {
	shell := ""
	for _, name := range []string{"pwsh", "powershell"} {
		if p, err := exec.LookPath(name); err == nil {
			shell = p
			break
		}
	}
	if shell == "" {
		t.Skip("no PowerShell on PATH")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	check := `$errs = $null; $null = [System.Management.Automation.Language.Parser]::ParseFile($env:RC_PS1, [ref]$null, [ref]$errs); ` +
		`if ($errs.Count -gt 0) { $errs | ForEach-Object { Write-Output $_.ToString() }; exit 1 }`
	cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", check) //nolint:gosec // a fixed command over a repository file
	cmd.Env = append(os.Environ(), "RC_PS1="+script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("install.ps1 does not parse: %v\n%s", err, out)
	}
}

// Runs install.sh for real, against a release laid out exactly as the workflow
// lays one out, with `curl` replaced by a script that serves it from disk.
// This is the test that would have caught `ln -sf X X`: the installer ran to
// the end only on paper.
func TestInstallShInstallsFromAReleaseArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is the macOS and Linux installer")
	}
	for _, tool := range []string{"sh", "tar", "awk", "uname"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}

	osName := map[string]string{"darwin": "darwin", "linux": "linux"}[runtime.GOOS]
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if osName == "" || arch == "" {
		t.Skipf("install.sh has no archive for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	// uname decides the archive, not the Go toolchain: an amd64 toolchain on
	// an arm64 machine still installs the arm64 archive.
	if out, err := exec.Command("uname", "-m").Output(); err == nil {
		switch strings.TrimSpace(string(out)) {
		case "x86_64", "amd64":
			arch = "amd64"
		case "arm64", "aarch64":
			arch = "arm64"
		}
	}
	// As install.sh does: a process under Rosetta on Apple silicon reports
	// x86_64, and the machine still gets the arm64 archive.
	if osName == "darwin" && arch == "amd64" {
		if out, err := exec.Command("sysctl", "-n", "sysctl.proc_translated").Output(); err == nil && strings.TrimSpace(string(out)) == "1" {
			arch = "arm64"
		}
	}

	const version = "v9.9.9"
	release := t.TempDir()
	archive := fmt.Sprintf("graphlume_%s_%s_%s.tar.gz", version, osName, arch)

	// The members the workflow packs, at the root, with a stand-in binary.
	var members []string
	for _, a := range workflowArchives(t) {
		if a.name == strings.Replace(archive, version, "${VERSION}", 1) {
			members = a.members
		}
	}
	if len(members) == 0 {
		t.Fatalf("release.yml builds no %s", archive)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		body, mode := []byte(m+"\n"), int64(0o644)
		if m == "graphlume" {
			body, mode = []byte("#!/bin/sh\necho \"graphlume "+version+" (stand-in)\"\n"), 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: m, Mode: mode, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, archive), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	// A second archive whose name contains the first, as a real SHA256SUMS
	// can: the lookup must not take its hash.
	sums := "0000000000000000000000000000000000000000000000000000000000000000  " + archive + ".sbom\n" +
		hex.EncodeToString(sum[:]) + "  " + archive + "\n"
	if err := os.WriteFile(filepath.Join(release, "SHA256SUMS"), []byte(sums), 0o600); err != nil {
		t.Fatal(err)
	}

	// curl -fsSL <url> -o <file>: serve the last path element from disk.
	bin := t.TempDir()
	fake := "#!/bin/sh\nurl=\"\"; out=\"\"\nwhile [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) out=\"$2\"; shift 2 ;;\n" +
		"    -*) shift ;;\n    *) url=\"$1\"; shift ;;\n  esac\ndone\n" +
		"cp \"" + release + "/${url##*/}\" \"$out\"\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(fake), 0o755); err != nil { //nolint:gosec // a test double that must be executable
		t.Fatal(err)
	}

	script, err := filepath.Abs(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("sh", script, "--dir", dest, "--version", version) //nolint:gosec // the repository's own installer
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, out)
	}

	installed := filepath.Join(dest, "graphlume")
	info, err := os.Lstat(installed)
	if err != nil {
		t.Fatalf("nothing was installed: %v\n%s", err, out)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("%s is %v, want an executable regular file\n%s", installed, info.Mode(), out)
	}
	ran, err := exec.Command(installed, "version").CombinedOutput() //nolint:gosec // the stand-in this test just installed
	if err != nil || !strings.Contains(string(ran), version) {
		t.Errorf("the installed command does not run: %v\n%s", err, ran)
	}
	if !strings.Contains(string(out), "graphlume "+version) {
		t.Errorf("install.sh did not finish by running the installed command:\n%s", out)
	}

	// And it refuses an archive whose checksum does not match.
	bad := strings.Replace(sums, hex.EncodeToString(sum[:]), strings.Repeat("1", 64), 1)
	if err := os.WriteFile(filepath.Join(release, "SHA256SUMS"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("sh", script, "--dir", filepath.Join(t.TempDir(), "bin"), "--version", version) //nolint:gosec // the repository's own installer
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "checksum mismatch") {
		t.Errorf("install.sh accepted an archive with the wrong checksum: %v\n%s", err, out)
	}
}
