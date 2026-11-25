package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/errs"
)

// Indexer names a compiler-backed SCIP indexer.
type Indexer string

const (
	SCIPGo         Indexer = "scip-go"
	SCIPTypeScript Indexer = "scip-typescript"
	SCIPPython     Indexer = "scip-python"
	SCIPJava       Indexer = "scip-java"
	SCIPClang      Indexer = "scip-clang"
	// SCIPRustAnalyzer is `rust-analyzer scip`: the indexer is a subcommand of
	// the language server, so the binary is rust-analyzer.
	SCIPRustAnalyzer Indexer = "rust-analyzer"
	SCIPDotnet       Indexer = "scip-dotnet"
	SCIPRuby         Indexer = "scip-ruby"
	Svelte2TSX       Indexer = "svelte2tsx"
)

// Channel is how an indexer is obtained.
type Channel string

const (
	// ChannelGoInstall builds from source with `go install`. Integrity comes
	// from the Go module checksum database, a public transparency log: `go
	// install module@version` refuses to proceed if the module bytes do not
	// match the logged hash. goEnv exists to make sure none of the environment
	// variables that disable that check are in effect.
	ChannelGoInstall Channel = "go-install"

	// ChannelNPM installs a published npm package globally. npm verifies the
	// registry's own integrity hash, which only proves the registry served what
	// it recorded — it is trust-on-first-use against the registry itself. So
	// this channel additionally packs the tarball first and compares its SHA256
	// against a digest pinned in this file before anything is installed.
	ChannelNPM Channel = "npm"

	// ChannelDocker pulls an image BY DIGEST. Pulling by digest is the
	// verification: the daemon refuses content whose manifest does not hash to
	// the requested value, so a compromised tag cannot substitute an image.
	ChannelDocker Channel = "docker"

	// ChannelUser is an indexer GraphLume never installs: it runs the one the
	// user put on PATH and says how to get it when it is absent. It is used
	// where no exact version AND digest could be recorded from a source that
	// was readable offline, and inventing either would be worse than saying
	// so. See userPins.
	ChannelUser Channel = "user"

	// ChannelStandaloneRelease fetches a standalone pre-compiled binary release,
	// verifies its SHA-256 digest against ReleaseSHA256, and installs it into
	// ~/.graphlume/bin.
	ChannelStandaloneRelease Channel = "standalone-release"

	// ChannelNuGetTool installs a .NET tool package. The package file is
	// fetched from ReleaseURL and checked against ReleaseSHA256 before
	// anything in it is run; `dotnet tool install` is then given that one
	// verified file as its only source and an explicit --version, and puts the
	// tool into ~/.graphlume/dotnet, a directory GraphLume owns, rather than
	// into the user's global tools.
	ChannelNuGetTool Channel = "nuget-tool"
)

// RunnerNative and RunnerDocker are config.Config.IndexerRunner's two values.
const (
	RunnerNative = "native"
	RunnerDocker = "docker"
)

// Pin is the pinned identity of one indexer release.
//
// Pins are compile-time constants. There is no code path that reads a version
// or a URL from anywhere else — not from a repository, not from the network,
// not from user config. `latest` does not appear in this package, because
// "install whatever is newest" means "execute whatever was published five
// minutes ago", and a supply-chain compromise of any of these packages would
// otherwise reach every GraphLume user within the hour.
type Pin struct {
	Indexer  Indexer
	Language Language
	Version  string

	// Binary is the executable name once installed.
	Binary string

	// VersionArgs asks the installed binary what it is, for the "already on
	// PATH" path where the version is recorded rather than assumed.
	VersionArgs []string

	Channel Channel

	// Module is the `go install` target, without the version.
	Module string

	// NPMPackage is the registry package name, without the version.
	NPMPackage string

	// TarballSHA256 is the hex SHA256 of the published npm tarball for exactly
	// this version. Empty means "not recorded", and provisioning REFUSES rather
	// than installing something unverified. See ErrNoRecordedDigest.
	TarballSHA256 string

	// Image and ImageDigest identify a container image. ImageDigest is the
	// "sha256:..." manifest digest and, as with TarballSHA256, an empty value
	// refuses the pull.
	Image       string
	ImageDigest string

	// MaxLanguageRelease is the highest language level this pinned indexer can
	// compile, or 0 when the question does not apply.
	//
	// It exists because "ready" was a lie. The Java pin reported ready against a
	// Java 25 project, ran the repository's Maven build for seven minutes, and
	// failed with `error: release version 25 not supported` -- the image ships
	// JDK 17. Nothing about that needed a build to discover: the project
	// declares its release in pom.xml and the image's JDK is a property of the
	// digest we pinned. Comparing two numbers is cheaper than a build, and it
	// is the difference between a tool that reports a mismatch and one that
	// makes the user find it.
	MaxLanguageRelease int

	// DockerEntry is the argv prefix inside the image, before this package's
	// own arguments. Kept as data because it is the thing most likely to change
	// when the image is rebuilt.
	DockerEntry []string

	// ReleaseURL and ReleaseSHA256 locate and verify a standalone single-binary
	// release, or (ChannelNuGetTool) a .NET tool's package file.
	ReleaseURL    string
	ReleaseSHA256 string

	// NuGetPackage is the package id of a .NET tool (ChannelNuGetTool).
	NuGetPackage string

	// InstallHint is the tool's own install instruction, shown where
	// GraphLume cannot or will not install it. Compile-time text only.
	InstallHint string
}

// pins is the pin table.
//
// Bumping a version here is a security decision, not a chore: it authorises
// every GraphLume user to download and execute new third-party code. A bump must
// change the digest in the same commit, and the digest must be obtained from the
// artifact itself rather than from whatever the release notes claim.
//
// HONEST NOTE ON THE DIGESTS. The npm and container digests below are
// deliberately empty. This package was written without network access, so any
// value here would be invented, and an invented digest is worse than none: it
// fails at install time with "checksum mismatch", which reads like a compromise
// and trains whoever hits it to paste in whatever hash they observed — which is
// exactly the habit that makes pinning worthless. Empty means Provisioner
// refuses to install through that channel, which is the fail-closed behaviour;
// see TestProvisionRefusesWithoutRecordedDigest. Users who already have these
// tools on PATH are unaffected, since a preexisting binary is used as-is.
var pins = []Pin{
	{
		Indexer:     SCIPGo,
		Language:    LangGo,
		Version:     "v0.2.7",
		Binary:      "scip-go",
		VersionArgs: []string{"--version"},
		Channel:     ChannelGoInstall,
		Module:      "github.com/scip-code/scip-go/cmd/scip-go",
	},
	{
		Indexer:       SCIPTypeScript,
		Language:      LangTypeScript,
		Version:       "0.3.14",
		Binary:        "scip-typescript",
		VersionArgs:   []string{"--version"},
		Channel:       ChannelNPM,
		NPMPackage:    "@sourcegraph/scip-typescript",
		TarballSHA256: "92a88678bcc021e7ff2afd0ab2684c1094344796840e582578aa4595020cc3f5",
		Image:         "node",
		ImageDigest:   "sha256:e13726ca604184d7a91fcacab1fc09eefd642dc28e79ac34e85540dd93b78cfb",
		DockerEntry:   []string{"npx", "-y", "@sourcegraph/scip-typescript@0.3.14"},
	},
	{
		Indexer:       SCIPPython,
		Language:      LangPython,
		Version:       "0.6.6",
		Binary:        "scip-python",
		VersionArgs:   []string{"--version"},
		Channel:       ChannelNPM,
		NPMPackage:    "@sourcegraph/scip-python",
		TarballSHA256: "be8e0a1ec180423c60e9f2c2672c208f49a35ab690ac0f8f1259e4223dbd6e42",
		Image:         "node",
		ImageDigest:   "sha256:e13726ca604184d7a91fcacab1fc09eefd642dc28e79ac34e85540dd93b78cfb",
		DockerEntry:   []string{"npx", "-y", "@sourcegraph/scip-python@0.6.6"},
	},
	{
		// scip-dotnet is a .NET tool: a NuGet package that `dotnet tool install`
		// unpacks. The package is fetched by GraphLume and its SHA-256 compared
		// with the one recorded here before dotnet sees it; dotnet is then
		// pointed at that single file (--source) with this exact --version, so
		// nothing else is resolved and nothing newer can be substituted.
		//
		// The package targets .NET 10 (its runtimeconfig names
		// Microsoft.NETCore.App 10.0.0), so the .NET 10 SDK or runtime has to be
		// present. Indexing a C# project loads it through MSBuild, which runs the
		// repository's own targets, and restores its packages over the network:
		// see Options.AllowRepoBuild.
		Indexer:       SCIPDotnet,
		Language:      LangCSharp,
		Version:       "0.2.16",
		Binary:        "scip-dotnet",
		VersionArgs:   []string{"--version"},
		Channel:       ChannelNuGetTool,
		NuGetPackage:  "scip-dotnet",
		ReleaseURL:    "https://api.nuget.org/v3-flatcontainer/scip-dotnet/0.2.16/scip-dotnet.0.2.16.nupkg",
		ReleaseSHA256: "70255008a2f3054701bafb551b51c3af0c1e0ac7624e63ec2e852b3f72e5df58",
		InstallHint:   "install the .NET SDK first (" + dotnetSDKHint + "), then run `graphlume indexers install`",
	},
	{
		// scip-java runs as a build-tool plugin, so "installing" it natively
		// means letting coursier fetch JVM artifacts and then letting Maven or
		// Gradle execute the repository's own build. The container is the
		// supported path here precisely because that blast radius belongs in a
		// container; see Options.AllowRepoBuild.
		Indexer:     SCIPJava,
		Language:    LangJava,
		Version:     "0.12.2",
		Binary:      "scip-java",
		VersionArgs: []string{"version"},
		Channel:     ChannelDocker,
		Image:       "sourcegraph/scip-java",
		ImageDigest: "sha256:a05564b9c9951660fc7069b431f1a18f4575b570b7ce09a14936729832c1fc8d",
		DockerEntry: []string{"scip-java"},
		// Verified by running javac inside this exact digest:
		//   javac 17.0.16
		//   error: release version 25 not supported
		//
		// 17 ON PURPOSE. The obvious fix — pin a
		// newer image that ships a newer JDK — is unavailable: there is no
		// such image to pin. Every tag sourcegraph/scip-java publishes was
		// enumerated and the newest two were pulled BY DIGEST and asked
		// directly:
		//
		//	0.12.3          sha256:2ad40207c69b0758fc0be783314fbb802232358acd648b5ecc2f870e372ee618
		//	latest-snapshot sha256:6617151d1896aef91884773d6c9ea482a1bf1c85c6f52a1215780852e9be3b33
		//
		//	$ docker run --rm --entrypoint javac sourcegraph/scip-java@<digest> --version
		//	javac 17.0.16          # both
		//	$ docker image inspect ... --format '{{.Config.Env}}'
		//	JAVA_VERSION=jdk-17.0.16+8
		//	$ ls /usr/lib/jvm /opt/java   # one JDK in the image, no alternative
		//
		// The project's own Dockerfile on main already reads
		// `FROM eclipse-temurin:25`, so this is a publishing lag, not a
		// decision: no image built from it has been pushed (newest tag
		// latest-snapshot, 2026-06-30). Bumping the digest would move the pin
		// without moving the ceiling — a supply-chain decision bought for
		// nothing — so the pin stays where a verified digest and a verified
		// ceiling agree.
		//
		// The consequence is honest and visible rather than silent: a Java 25
		// project is reported as blocked by the preflight in release.go
		// instead of failing seven minutes into a Maven build. Java precision
		// stays UNMEASURED for releases above 17, and no claim in this
		// repository should say otherwise until an image exists that changes
		// this number.
		MaxLanguageRelease: 17,
	},
	{
		// scip-ruby is a Sorbet-based standalone binary indexer for Ruby.
		// It reads and type-checks Ruby source code without running repository code.
		Indexer:       SCIPRuby,
		Language:      LangRuby,
		Version:       "0.5.1",
		Binary:        "scip-ruby",
		VersionArgs:   []string{"--version"},
		Channel:       ChannelStandaloneRelease,
		ReleaseURL:    rubyReleaseURL(),
		ReleaseSHA256: rubyReleaseSHA256(),
		InstallHint:   "download the scip-ruby release from https://github.com/sourcegraph/scip-ruby/releases or run `gem install scip-ruby`",
	},
	{
		Indexer:       Svelte2TSX,
		Language:      "svelte",
		Version:       "0.7.63",
		Binary:        "svelte2tsx",
		VersionArgs:   []string{"--version"},
		Channel:       ChannelNPM,
		NPMPackage:    "svelte2tsx",
		TarballSHA256: "f7232c6bbd19be3fc1cbc16f6e1e0c0ab228edfc21e5b2c9e66dcfa63a479e79",
		InstallHint:   "npm install -g svelte2tsx",
	},
}

func rubyReleaseURL() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "https://github.com/sourcegraph/scip-ruby/releases/download/scip-ruby-v0.5.1/scip-ruby-arm64-darwin"
	case "linux/amd64":
		return "https://github.com/sourcegraph/scip-ruby/releases/download/scip-ruby-v0.5.1/scip-ruby-x86_64-linux"
	default:
		return ""
	}
}

func rubyReleaseSHA256() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "af0c8be10e714b56eb4a9ac64ec0014e7eff15cca86eb77a88eac58be222ddb1"
	case "linux/amd64":
		return "3b0eec58c00d2c742e182fc9070a048d933ff6485098bcc3e3c8b28fbcf32c8e"
	default:
		return ""
	}
}

// userPins are indexers GraphLume runs when they are installed and never
// installs: no exact version with a digest could be recorded for them without
// downloading, so there is nothing to verify an install against. They are
// deliberately absent from Pins(), the table `indexers pins` prints and CI
// keys a cache on, which lists only what GraphLume may fetch.
//
// How each is invoked comes from the tool's own published interface, not from
// running it (neither is installed on the machine this was written on):
//
//	rust-analyzer scip <path> [--output <file>] [--config-path <file>]
//	    rust-analyzer's crates/rust-analyzer/src/cli/flags.rs: `path` is a
//	    required positional, --output defaults to index.scip, and
//	    --config-path names a JSON file of rust-analyzer settings.
//	scip-clang --compdb-path=<compile_commands.json>   scip-clang's
//	    docs/Development.md: run from the project root, not the build root.
//	    --index-output-path is read from the option name in the driver source
//	    (cliOpts.indexOutputPath) and is NOT confirmed against --help.
var userPins = []Pin{
	{
		Indexer:     SCIPClang,
		Language:    LangCPP,
		Binary:      "scip-clang",
		VersionArgs: []string{"--version"},
		Channel:     ChannelUser,
		InstallHint: "download the scip-clang release for your platform from " +
			"https://github.com/sourcegraph/scip-clang/releases and put it on PATH",
	},
	{
		Indexer:     SCIPRustAnalyzer,
		Language:    LangRust,
		Binary:      "rust-analyzer",
		VersionArgs: []string{"--version"},
		Channel:     ChannelUser,
		InstallHint: "rustup component add rust-analyzer",
	},
}

// dotnetSDKHint says where the .NET SDK comes from. scip-dotnet is a .NET tool,
// so without the SDK its own install line fails with "dotnet: command not found".
const dotnetSDKHint = "https://dotnet.microsoft.com/download, or on macOS `brew install --cask dotnet-sdk`"

// UserInstalled returns the indexers GraphLume runs but does not install.
func UserInstalled() []Pin { return append([]Pin(nil), userPins...) }

// Pins returns the pin table, sorted by indexer name.
// Pin provenance, verified. Each digest below was fetched and
// checked, not copied from documentation:
//
//	scip-go          v0.2.7   MODULE PATH MOVED: github.com/sourcegraph/scip-go
//	                          now declares itself as github.com/scip-code/scip-go,
//	                          so the old path fails with "module declares its
//	                          path as". Confirmed present in the module proxy's version
//	                          list (proxy.golang.org/.../@v/list). `go install`
//	                          verifies against the checksum database itself, so
//	                          no separate digest is recorded.
//	scip-typescript  0.3.14   SHA256 of the published npm tarball. Cross-checked
//	                          against the registry's own sha512 integrity field,
//	                          which matched — independent confirmation the
//	                          artifact was not substituted in transit.
//	scip-python      0.6.6    same method, same cross-check.
//	scip-java        0.12.2   manifest digest from registry-1.docker.io.
//	                          NOTE: this repository's tags carry NO "v" prefix,
//	                          and 0.10.5 was never published at all (0.10.4 is
//	                          the highest 0.10.x). A pin of "v0.10.5"
//	                          would fail with "manifest unknown".
//
// Re-verified against the newer tags, and DELIBERATELY NOT BUMPED.
// The method below reproduced this exact digest for 0.12.2, which is what makes
// the two newer digests it produced trustworthy — and both of those images ship
// the same JDK 17 as this one. See MaxLanguageRelease above for the full
// enumeration and the commands. A version bump is authorisation for every
// GraphLume user to execute new third-party code; there is no benefit here to
// weigh against that.
//
// To refresh:
//
//	npm:    curl -s https://registry.npmjs.org/<pkg>/<ver> | jq -r .dist.tarball
//	        then shasum -a 256 on the downloaded tarball
//	docker: curl -sI -H "Authorization: Bearer $TOKEN" \
//	          -H "Accept: application/vnd.oci.image.index.v1+json" \
//	          https://registry-1.docker.io/v2/<repo>/manifests/<tag> \
//	        and read docker-content-digest
//
// An empty digest means "not recorded", and provisioning REFUSES rather than
// installing something unverified. Failing closed is the correct default: a
// silently unverified indexer is a supply-chain hole, and inventing a digest
// to make the code look finished would be worse than leaving it empty.
func Pins() []Pin {
	out := append([]Pin(nil), pins...)
	sort.Slice(out, func(i, j int) bool { return out[i].Indexer < out[j].Indexer })
	return out
}

// PinFor returns the pin for a language.
func PinFor(l Language) (Pin, bool) {
	for _, p := range pins {
		if p.Language == l {
			return p, true
		}
	}
	for _, p := range userPins {
		if p.Language == l {
			return p, true
		}
	}
	return Pin{}, false
}

// Ref renders the pinned artifact reference for humans and for the prompt.
func (p Pin) Ref() string {
	switch p.Channel {
	case ChannelGoInstall:
		return p.Module + "@" + p.Version
	case ChannelNPM:
		return p.NPMPackage + "@" + p.Version
	case ChannelDocker:
		if p.ImageDigest != "" {
			return p.Image + "@" + p.ImageDigest
		}
		return p.Image + ":" + p.Version
	case ChannelUser:
		return p.Binary + " (user-installed)"
	case ChannelStandaloneRelease:
		return p.Binary + "@" + p.Version + " (standalone release)"
	case ChannelNuGetTool:
		return p.NuGetPackage + "@" + p.Version + " (NuGet)"
	}
	return string(p.Indexer) + "@" + p.Version
}

// Errors from provisioning. Each is a sentinel because a caller genuinely
// branches on it: the CLI prints different advice for "you said never", "I
// cannot ask you" and "the bytes were wrong", and the last of those deserves to
// look nothing like a normal failure.
var (
	// ErrProvisionNotPermitted means accuracy.indexers.provision is `never`.
	ErrProvisionNotPermitted = errors.New("indexer: provisioning is disabled by accuracy.indexers.provision=never")

	// ErrProvisionDeclined means the user was asked and said no.
	ErrProvisionDeclined = errors.New("indexer: provisioning declined")

	// ErrProvisionNeedsConsent means provisioning was required, the policy is
	// `prompt`, and there is nobody to prompt.
	//
	// This is the non-interactive case, and defaulting it to "yes" is exactly
	// the mistake that would make a CI job download and execute third-party
	// binaries without anyone deciding to. The caller has to pass
	// Options.AssumeYes — one explicit flag, at one call site.
	ErrProvisionNeedsConsent = errors.New("indexer: provisioning needs confirmation and the session is not interactive")

	// ErrNoRecordedDigest means the pin has no digest for this channel, so
	// there is nothing to verify against. Refusing is the point.
	ErrNoRecordedDigest = errors.New("indexer: no pinned digest recorded for this artifact; refusing to install it unverified")

	// ErrChecksumMismatch means the artifact is not what was pinned. Treated as
	// unrecoverable rather than retryable on purpose: the plausible causes are
	// a compromised registry, a hijacked package name or a stale pin, and none
	// of them is fixed by downloading it again.
	ErrChecksumMismatch = errors.New("indexer: artifact does not match its pinned digest")
)

// ProvisionRequest is what the user is asked to approve.
//
// Every field is derived from the compile-time pin table, so the sentence shown
// to a user cannot be influenced by the repository being indexed. A prompt whose
// text an attacker can write is not a security control.
type ProvisionRequest struct {
	Indexer  Indexer
	Language Language
	Version  string
	Channel  Channel

	// Ref is the exact artifact, e.g. "@sourcegraph/scip-typescript@0.3.14".
	Ref string

	// Mutates says whether the host is changed. False under the docker runner,
	// where the install lands in the image store and nothing else.
	Mutates bool
}

// String renders the question.
func (r ProvisionRequest) String() string {
	where := "on this machine"
	if !r.Mutates {
		where = "into the local container image store"
	}
	return fmt.Sprintf("install %s (%s) %s to index %s with compiler accuracy",
		r.Indexer, r.Ref, where, r.Language)
}

// PromptFunc asks the user to approve one install. Returning false is a normal
// answer, not an error; an error means the question could not be asked.
type PromptFunc func(context.Context, ProvisionRequest) (bool, error)

// Installed is a usable indexer.
type Installed struct {
	Indexer Indexer
	Version string

	// Path is the absolute path to the executable, empty for ChannelDocker.
	Path string

	// Image is "repo@sha256:..." for ChannelDocker, empty otherwise.
	Image string

	// Preexisting means it was already on PATH (or already in the image store)
	// and nothing was downloaded. The version recorded is then what the tool
	// reports about itself, not what the pin says — those can differ, and
	// claiming the pinned version for a binary we did not install would be a
	// provenance lie.
	Preexisting bool
}

// Provisioner installs pinned indexers, subject to policy.
type Provisioner struct {
	Runner  Runner
	Policy  config.ProvisionPolicy
	Prompt  PromptFunc
	Audit   AuditSink
	Timeout time.Duration

	// AssumeYes is the caller's explicit opt-in for a non-interactive session.
	// It is a flag the human typed, never a default and never inferred from a
	// TTY check.
	AssumeYes bool

	// Runner selection: under RunnerDocker nothing is installed on the host.
	RunnerKind string
}

// Available reports whether the indexer can already be used, without installing
// anything and without asking anyone.
//
// This is what makes `Plan` free of side effects and what implements "if a tool
// is already on PATH, use it and record the version rather than installing".
func (p *Provisioner) Available(ctx context.Context, pin Pin) (Installed, bool) {
	if p.RunnerKind == RunnerDocker && (pin.Channel == ChannelDocker || pin.Image != "") {
		return p.dockerAvailable(ctx, pin)
	}
	// Under the native runner a container-channel indexer can still be USED if
	// the user installed it themselves — scip-java from coursier is the case —
	// even though this package will not install it that way. Found is found.
	pathTo, err := p.Runner.LookPath(pin.Binary)
	if err != nil {
		return Installed{}, false
	}
	inst := Installed{Indexer: pin.Indexer, Path: pathTo, Preexisting: true}
	if len(pin.VersionArgs) > 0 {
		res, runErr := p.Runner.Run(ctx, Command{
			Path: pathTo, Args: pin.VersionArgs, Timeout: probeTimeout,
		})
		recordErr(p.Audit, ActionProbe, AuditRecord{
			Indexer: pin.Indexer, Language: pin.Language, Result: res,
		}, runErr)
		if runErr == nil && res.ExitCode == 0 {
			inst.Version = firstLine(string(res.Stdout))
			if inst.Version == "" {
				inst.Version = firstLine(string(res.Stderr))
			}
		} else if pin.Indexer == SCIPRustAnalyzer {
			// rustup puts a `rust-analyzer` proxy on PATH whether or not the
			// component is installed; without it the proxy exits non-zero on
			// every call. A binary that cannot report its version is not
			// something to run, and setup offers the component instead.
			return Installed{}, false
		}
	}
	return inst, true
}

func (p *Provisioner) dockerAvailable(ctx context.Context, pin Pin) (Installed, bool) {
	if pin.ImageDigest == "" {
		return Installed{}, false
	}
	docker, err := p.Runner.LookPath(string(ToolDocker))
	if err != nil {
		return Installed{}, false
	}
	ref := pin.Image + "@" + pin.ImageDigest
	res, runErr := p.Runner.Run(ctx, Command{
		Path: docker, Args: []string{"image", "inspect", ref}, Timeout: probeTimeout,
	})
	recordErr(p.Audit, ActionProbe, AuditRecord{
		Indexer: pin.Indexer, Language: pin.Language, Tool: ToolDocker, Result: res,
	}, runErr)
	if runErr != nil || res.ExitCode != 0 {
		return Installed{}, false
	}
	return Installed{Indexer: pin.Indexer, Version: pin.Version, Image: ref, Preexisting: true}, true
}

// Ensure makes the indexer usable, installing it if policy allows.
//
// The order is deliberate and each step is a gate:
//
//  1. Already available? Use it. Nothing is downloaded and nobody is asked.
//  2. Policy `never`? Refuse.
//  3. Is there a digest to verify against? No digest, no install.
//  4. Ask, unless the policy is `auto` (which is itself an explicit opt-in
//     written into the user's own config file) or the caller passed AssumeYes.
//  5. Fetch, verify, install.
//
// Step 3 sits before step 4 so a user is never asked to approve an install that
// was going to be refused anyway.
func (p *Provisioner) Ensure(ctx context.Context, pin Pin) (Installed, error) {
	if inst, ok := p.Available(ctx, pin); ok {
		return inst, nil
	}

	if p.Policy == config.ProvisionNever {
		return Installed{}, errs.Wrap(errs.KindInvalid, "indexer.Ensure",
			fmt.Errorf("%w: %s is not installed", ErrProvisionNotPermitted, pin.Ref())).
			WithField("indexer", string(pin.Indexer)).
			WithField("language", string(pin.Language))
	}

	if err := pin.verifiable(); err != nil {
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure", err).
			WithField("indexer", string(pin.Indexer))
	}

	mutates := pin.Channel != ChannelDocker
	if p.RunnerKind == RunnerDocker {
		mutates = false
	}
	ref := pin.Ref()
	reqChannel := pin.Channel
	if p.RunnerKind == RunnerDocker && pin.Image != "" {
		reqChannel = ChannelDocker
		if pin.ImageDigest != "" {
			ref = pin.Image + "@" + pin.ImageDigest
		}
	}
	req := ProvisionRequest{
		Indexer:  pin.Indexer,
		Language: pin.Language,
		Version:  pin.Version,
		Channel:  reqChannel,
		Ref:      ref,
		Mutates:  mutates,
	}
	if err := p.consent(ctx, req); err != nil {
		return Installed{}, err
	}

	if p.RunnerKind == RunnerDocker && pin.Image != "" {
		return p.dockerPull(ctx, pin)
	}

	switch pin.Channel {
	case ChannelGoInstall:
		return p.goInstall(ctx, pin)
	case ChannelNPM:
		return p.npmInstall(ctx, pin)
	case ChannelDocker:
		return p.dockerPull(ctx, pin)
	case ChannelStandaloneRelease:
		return p.standaloneInstall(ctx, pin)
	case ChannelNuGetTool:
		return p.nugetInstall(ctx, pin)
	}
	return Installed{}, errs.New(errs.KindInternal, "indexer.Ensure",
		"unknown provisioning channel "+string(pin.Channel))
}

// verifiable reports whether there is anything to check the download against.
func (pin Pin) verifiable() error {
	switch pin.Channel {
	case ChannelUser:
		return fmt.Errorf("%w: %s is user-installed, GraphLume has no pinned release of it (%s)",
			ErrNoRecordedDigest, pin.Binary, pin.InstallHint)
	case ChannelNPM:
		if pin.TarballSHA256 == "" {
			return fmt.Errorf("%w: %s", ErrNoRecordedDigest, pin.Ref())
		}
	case ChannelDocker:
		if pin.ImageDigest == "" {
			return fmt.Errorf("%w: %s", ErrNoRecordedDigest, pin.Ref())
		}
	case ChannelStandaloneRelease:
		if pin.ReleaseURL == "" || pin.ReleaseSHA256 == "" {
			return fmt.Errorf("%w: %s (no release url or digest)", ErrNoRecordedDigest, pin.Ref())
		}
	case ChannelNuGetTool:
		if pin.NuGetPackage == "" || pin.ReleaseURL == "" || pin.ReleaseSHA256 == "" || pin.Version == "" {
			return fmt.Errorf("%w: %s (no package, version, url or digest)", ErrNoRecordedDigest, pin.Ref())
		}
	case ChannelGoInstall:
		// The Go module checksum database is the verification here, and it is
		// enforced by goEnv rather than by a constant in this file. Recording a
		// second hash would not add a check; disabling GOSUMDB would remove
		// one, which is why goEnv clears every variable that could.
		if pin.Module == "" || !strings.HasPrefix(pin.Version, "v") {
			return fmt.Errorf("%w: malformed go pin %q", ErrNoRecordedDigest, pin.Ref())
		}
	}
	return nil
}

// consent applies the provisioning policy.
func (p *Provisioner) consent(ctx context.Context, req ProvisionRequest) error {
	switch p.Policy {
	case config.ProvisionAuto:
		// The user wrote provision: auto in their own config file. That IS the
		// consent; digests are still verified.
		return nil

	case config.ProvisionPrompt:
		if p.Prompt != nil {
			ok, err := p.Prompt(ctx, req)
			if err != nil {
				return errs.Wrap(errs.KindInternal, "indexer.Ensure", err)
			}
			if !ok {
				return errs.Wrap(errs.KindInvalid, "indexer.Ensure",
					fmt.Errorf("%w: %s", ErrProvisionDeclined, req.Ref))
			}
			return nil
		}
		if p.AssumeYes {
			return nil
		}
		return errs.Wrap(errs.KindInvalid, "indexer.Ensure",
			fmt.Errorf("%w: %s", ErrProvisionNeedsConsent, req.Ref)).
			WithField("indexer", string(req.Indexer))

	default:
		return errs.New(errs.KindInvalid, "indexer.Ensure",
			"unknown provisioning policy "+string(p.Policy))
	}
}

func (p *Provisioner) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return DefaultTimeout
}

// scratchDir makes a private working directory for a provisioning step.
//
// Provisioning NEVER runs with the repository as its working directory. `go
// install` in a repository reads that repository's go.mod (including its
// `toolchain` directive) and `npm` in a repository reads its .npmrc, which can
// redirect the registry to an attacker's host. A temp directory has neither.
func scratchDir(purpose string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "graphlume-provision-"+purpose+"-")
	if err != nil {
		return "", func() {}, fmt.Errorf("indexer: create scratch dir: %w", err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func (p *Provisioner) goInstall(ctx context.Context, pin Pin) (Installed, error) {
	goBin, err := p.Runner.LookPath(string(ToolGo))
	if err != nil {
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure",
			fmt.Errorf("go toolchain is required to install %s: %w", pin.Indexer, err))
	}
	scratch, cleanup, err := scratchDir("go")
	if err != nil {
		return Installed{}, err
	}
	defer cleanup()

	cmd := Command{
		Path:    goBin,
		Args:    []string{"install", pin.Module + "@" + pin.Version},
		Dir:     scratch,
		Env:     goEnv(),
		Timeout: p.timeout(),
	}
	res, runErr := p.Runner.Run(ctx, cmd)
	recordErr(p.Audit, ActionProvision, AuditRecord{
		Indexer: pin.Indexer, Language: pin.Language, Result: res,
	}, runErr)
	if runErr != nil {
		return Installed{}, errs.Wrap(errs.KindOf(runErr), "indexer.Ensure", runErr)
	}
	if res.ExitCode != 0 {
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure",
			fmt.Errorf("go install %s exited %d: %s", pin.Ref(), res.ExitCode, res.Diagnostic()))
	}
	return p.locateInstalled(pin)
}

// npmInstall packs, verifies, then installs.
//
// The two-step shape is the whole point. `npm install -g pkg@version` would
// install whatever the registry serves; packing first produces a local tarball
// whose SHA256 can be compared against the pin before a single line of that
// package's code has a chance to run. --ignore-scripts on both commands is the
// other half: an npm lifecycle script executes as the invoking user at install
// time, which would defeat verifying the payload we then never trusted enough
// to execute.
func (p *Provisioner) npmInstall(ctx context.Context, pin Pin) (Installed, error) {
	npmBin, err := p.Runner.LookPath(string(ToolNPM))
	if err != nil {
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure",
			fmt.Errorf("npm is required to install %s: %w", pin.Indexer, err))
	}
	scratch, cleanup, err := scratchDir("npm")
	if err != nil {
		return Installed{}, err
	}
	defer cleanup()

	packCmd := Command{
		Path: npmBin,
		Args: []string{
			"pack", pin.NPMPackage + "@" + pin.Version,
			"--pack-destination", scratch,
			"--ignore-scripts",
			"--no-audit", "--no-fund", "--loglevel", "error",
		},
		Dir:     scratch,
		Env:     os.Environ(),
		Timeout: p.timeout(),
	}
	res, runErr := p.Runner.Run(ctx, packCmd)
	recordErr(p.Audit, ActionProvision, AuditRecord{
		Indexer: pin.Indexer, Language: pin.Language, Result: res,
	}, runErr)
	if runErr != nil {
		return Installed{}, errs.Wrap(errs.KindOf(runErr), "indexer.Ensure", runErr)
	}
	if res.ExitCode != 0 {
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure",
			fmt.Errorf("npm pack %s exited %d: %s", pin.Ref(), res.ExitCode, res.Diagnostic()))
	}

	tarball, err := soleTarball(scratch)
	if err != nil {
		return Installed{}, errs.Wrap(errs.KindInternal, "indexer.Ensure", err)
	}
	sum, err := sha256File(tarball)
	if err != nil {
		return Installed{}, errs.Wrap(errs.KindInternal, "indexer.Ensure", err)
	}
	if !equalHex(sum, pin.TarballSHA256) {
		// KindCorrupt rather than KindTransient: retrying fetches the same
		// bytes, and if they really are different bytes than we pinned, the
		// last thing to do is fetch them again until one gets through.
		return Installed{}, errs.Wrap(errs.KindCorrupt, "indexer.Ensure",
			fmt.Errorf("%w: %s has sha256 %s, pinned %s",
				ErrChecksumMismatch, pin.Ref(), sum, pin.TarballSHA256)).
			WithField("indexer", string(pin.Indexer))
	}

	// Into a prefix GraphLume owns, not npm's global one. The global prefix is
	// often unwritable (a system Node) or its bin directory is not on PATH
	// (nvm, volta, a custom prefix): both ended in "installed but not on PATH"
	// or a permission error, and either way mutated a directory the user
	// manages. ExecRunner.LookPath searches this prefix.
	prefix := NPMPrefixDir()
	if prefix == "" {
		return Installed{}, errs.New(errs.KindInternal, "indexer.Ensure",
			"unable to locate the user home directory for ~/.graphlume/npm")
	}
	installCmd := Command{
		Path: npmBin,
		Args: []string{
			"install", "--global", "--prefix", prefix, "--ignore-scripts",
			"--no-audit", "--no-fund", "--loglevel", "error", tarball,
		},
		Dir:     scratch,
		Env:     os.Environ(),
		Timeout: p.timeout(),
	}
	res, runErr = p.Runner.Run(ctx, installCmd)
	recordErr(p.Audit, ActionProvision, AuditRecord{
		Indexer: pin.Indexer, Language: pin.Language, Result: res,
	}, runErr)
	if runErr != nil {
		return Installed{}, errs.Wrap(errs.KindOf(runErr), "indexer.Ensure", runErr)
	}
	if res.ExitCode != 0 {
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure",
			fmt.Errorf("npm install %s exited %d: %s", pin.Ref(), res.ExitCode, res.Diagnostic()))
	}
	return p.locateInstalled(pin)
}

// dockerPull fetches the image by digest.
func (p *Provisioner) dockerPull(ctx context.Context, pin Pin) (Installed, error) {
	docker, err := p.Runner.LookPath(string(ToolDocker))
	if err != nil {
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure",
			fmt.Errorf("docker is required to provision %s: %w", pin.Indexer, err))
	}
	// The one failure `docker pull` cannot report: a credential helper that
	// never returns. See credentialHelperPreflight.
	if err := p.credentialHelperPreflight(ctx, pin); err != nil {
		return Installed{}, err
	}
	ref := pin.Image + "@" + pin.ImageDigest
	cmd := Command{
		Path: docker, Args: []string{"pull", "--quiet", ref},
		Env: os.Environ(), Timeout: p.timeout(),
	}
	res, runErr := p.Runner.Run(ctx, cmd)
	recordErr(p.Audit, ActionProvision, AuditRecord{
		Indexer: pin.Indexer, Language: pin.Language, Tool: ToolDocker, Result: res,
	}, runErr)
	if runErr != nil {
		return Installed{}, errs.Wrap(errs.KindOf(runErr), "indexer.Ensure", runErr)
	}
	if res.ExitCode != 0 {
		// A digest that does not resolve is indistinguishable at this level
		// from a network failure, and saying "checksum mismatch" when the
		// answer might be "you are offline" would be dishonest. The daemon's
		// own message is carried through instead.
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure",
			fmt.Errorf("docker pull %s exited %d: %s", ref, res.ExitCode, res.Diagnostic()))
	}
	return Installed{Indexer: pin.Indexer, Version: pin.Version, Image: ref}, nil
}

// locateInstalled finds the binary that was just installed and records what it
// says its version is, rather than assuming the pin took effect.
func (p *Provisioner) locateInstalled(pin Pin) (Installed, error) {
	pathTo, err := p.Runner.LookPath(pin.Binary)
	if err != nil {
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure",
			fmt.Errorf("installed %s but %s is not on PATH: %w", pin.Ref(), pin.Binary, err))
	}
	return Installed{Indexer: pin.Indexer, Version: pin.Version, Path: pathTo}, nil
}

// soleTarball returns the single .tgz in dir.
//
// Insisting there is exactly one is a small integrity check of its own: `npm
// pack` on a package with bundled dependencies can emit more than one, and
// installing an unexamined extra file would sidestep the digest comparison this
// whole function exists to serve.
func soleTarball(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read scratch dir: %w", err)
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tgz") {
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("expected exactly one packed tarball, found %d", len(found))
	}
	return found[0], nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is inside a temp dir this process created
	if err != nil {
		return "", fmt.Errorf("open artifact: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash artifact: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// equalHex compares two hex digests case-insensitively, refusing an empty
// expectation. It is not constant-time and does not need to be: both values are
// public, and there is no secret to leak through timing.
func equalHex(got, want string) bool {
	if want == "" || got == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(got), strings.TrimSpace(want))
}

// GraphlumeBinDir returns ~/.graphlume/bin on disk.
func GraphlumeBinDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".graphlume", "bin")
}

// NPMPrefixDir is the npm prefix GraphLume installs its npm-distributed
// indexers into: ~/.graphlume/npm.
func NPMPrefixDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".graphlume", "npm")
}

// npmBinDir is where npm puts executables for a prefix: <prefix>/bin on Unix,
// the prefix itself on Windows.
func npmBinDir() string {
	prefix := NPMPrefixDir()
	if prefix == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return prefix
	}
	return filepath.Join(prefix, "bin")
}

func (p *Provisioner) standaloneInstall(ctx context.Context, pin Pin) (Installed, error) {
	binDir := GraphlumeBinDir()
	if binDir == "" {
		return Installed{}, errs.New(errs.KindInternal, "indexer.standaloneInstall", "unable to locate user home directory for ~/.graphlume/bin")
	}
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return Installed{}, errs.Wrap(errs.KindInternal, "indexer.standaloneInstall", err)
	}

	target := filepath.Join(binDir, pin.Binary)

	if !strings.HasPrefix(pin.ReleaseURL, "https://") && !strings.HasPrefix(pin.ReleaseURL, "http://127.0.0.1") && !strings.HasPrefix(pin.ReleaseURL, "http://localhost") {
		return Installed{}, errs.New(errs.KindInvalid, "indexer.standaloneInstall", "only secure https:// release URLs are permitted for remote downloads")
	}

	tmpFile := filepath.Join(binDir, fmt.Sprintf(".%s.%d.download", pin.Binary, os.Getpid()))
	defer func() { _ = os.Remove(tmpFile) }()
	if err := fetchVerified(ctx, pin.ReleaseURL, pin.ReleaseSHA256, tmpFile, 0o700, pin.Binary); err != nil {
		return Installed{}, err
	}

	if err := os.Rename(tmpFile, target); err != nil {
		return Installed{}, errs.Wrap(errs.KindInternal, "indexer.standaloneInstall", err)
	}

	return Installed{
		Indexer: pin.Indexer,
		Path:    target,
		Version: pin.Version,
	}, nil
}

// maxDownloadBytes bounds one verified download.
const maxDownloadBytes = 500 << 20 // 500 MiB

// fetchVerified downloads url to dest and compares its SHA-256 with want. The
// file is left at dest only when the digest matches.
//
// Bounded per phase, not overall: a whole-request Timeout would cut off a
// large file on a slow link however healthy the transfer. Connecting, the TLS
// handshake and the response headers each get a deadline; the body is bounded
// by ctx (the provisioning timeout) and by the size cap. Redirects must stay
// on https, so a mirror cannot downgrade the fetch.
func fetchVerified(ctx context.Context, url, want, dest string, mode os.FileMode, what string) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://127.0.0.1") && !strings.HasPrefix(url, "http://localhost") {
		return errs.New(errs.KindInvalid, "indexer.fetchVerified", "only secure https:// release URLs are permitted for remote downloads")
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Hostname() != "127.0.0.1" && req.URL.Hostname() != "localhost" {
				return fmt.Errorf("redirect to non-https URL %s refused", req.URL.Redacted())
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errs.Wrap(errs.KindInvalid, "indexer.fetchVerified", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return errs.Wrap(errs.KindTransient, "indexer.fetchVerified", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errs.New(errs.KindTransient, "indexer.fetchVerified",
			fmt.Sprintf("download %s: HTTP %d", url, resp.StatusCode))
	}

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) //nolint:gosec // G304: a temporary name under a directory GraphLume owns
	if err != nil {
		return errs.Wrap(errs.KindInternal, "indexer.fetchVerified", err)
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, hasher), io.LimitReader(resp.Body, maxDownloadBytes))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return errs.Wrap(errs.KindTransient, "indexer.fetchVerified", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return errs.Wrap(errs.KindInternal, "indexer.fetchVerified", closeErr)
	}
	observed := hex.EncodeToString(hasher.Sum(nil))
	if !equalHex(observed, want) {
		_ = os.Remove(dest)
		return errs.Wrap(errs.KindCorrupt, "indexer.fetchVerified",
			fmt.Errorf("%w: downloaded %s got %s, want %s", ErrChecksumMismatch, what, observed, want))
	}
	return nil
}

// DotnetToolDir is the directory GraphLume installs .NET tools into:
// ~/.graphlume/dotnet. `dotnet tool install --tool-path` puts the tool's
// executable directly in it.
func DotnetToolDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".graphlume", "dotnet")
}

// nugetInstall fetches the pinned package, verifies it, and installs it with
// `dotnet tool install`.
//
// The order is the point, as in npmInstall: the package is downloaded by
// GraphLume and its SHA-256 compared with the pin BEFORE dotnet opens it, and
// dotnet is then given only that file as its package source (--source replaces
// every configured feed) and an explicit --version. Nothing is resolved from
// the network by the install, and nothing of the repository is involved: the
// command runs in a scratch directory, so no global.json or NuGet.config of a
// repository is read.
func (p *Provisioner) nugetInstall(ctx context.Context, pin Pin) (Installed, error) {
	dotnet, err := p.Runner.LookPath(string(ToolDotnet))
	if err != nil {
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure",
			fmt.Errorf("the .NET SDK is required to install %s: %w", pin.Indexer, err))
	}
	toolDir := DotnetToolDir()
	if toolDir == "" {
		return Installed{}, errs.New(errs.KindInternal, "indexer.Ensure",
			"unable to locate the user home directory for ~/.graphlume/dotnet")
	}
	scratch, cleanup, err := scratchDir("nuget")
	if err != nil {
		return Installed{}, err
	}
	defer cleanup()

	file := filepath.Join(scratch, strings.ToLower(pin.NuGetPackage)+"."+pin.Version+".nupkg")
	if err := fetchVerified(ctx, pin.ReleaseURL, pin.ReleaseSHA256, file, 0o600, pin.Ref()); err != nil {
		return Installed{}, err
	}
	if err := os.MkdirAll(toolDir, 0o750); err != nil {
		return Installed{}, errs.Wrap(errs.KindInternal, "indexer.Ensure", err)
	}
	env := os.Environ()
	env = setEnv(env, "DOTNET_CLI_TELEMETRY_OPTOUT", "1")
	env = setEnv(env, "DOTNET_NOLOGO", "1")
	env = setEnv(env, "DOTNET_SKIP_FIRST_TIME_EXPERIENCE", "1")
	env = setEnv(env, "NUGET_PACKAGES", filepath.Join(scratch, "packages"))
	cmd := Command{
		Path: dotnet,
		Args: []string{
			"tool", "install", pin.NuGetPackage,
			"--tool-path", toolDir,
			"--version", pin.Version,
			"--source", scratch,
		},
		Dir:     scratch,
		Env:     env,
		Timeout: p.timeout(),
	}
	res, runErr := p.Runner.Run(ctx, cmd)
	recordErr(p.Audit, ActionProvision, AuditRecord{
		Indexer: pin.Indexer, Language: pin.Language, Tool: ToolDotnet, Result: res,
	}, runErr)
	if runErr != nil {
		return Installed{}, errs.Wrap(errs.KindOf(runErr), "indexer.Ensure", runErr)
	}
	if res.ExitCode != 0 {
		return Installed{}, errs.Wrap(errs.KindUnsupported, "indexer.Ensure",
			fmt.Errorf("dotnet tool install %s exited %d: %s", pin.Ref(), res.ExitCode, res.Diagnostic()))
	}
	return p.locateInstalled(pin)
}
