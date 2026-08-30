package update

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The Makefile names the release tarballs and the updater goes looking for
// them, and the two agree only by convention: nothing links the shell string in
// one to the format string in the other.
//
// They disagreed once. VERSION came from `git describe --tags`, which returns
// v0.8.0 at a tag, so `make release` would have written
// netrewind-v0.8.0-linux-amd64.tar.gz while the updater trims the v and asks
// for netrewind-0.8.0-linux-amd64.tar.gz. The 0.8.0 release happened to be
// built with VERSION overridden by hand, so the published assets were right and
// the bug stayed invisible - waiting for the first release built the obvious
// way, which would have left every installed recorder unable to find its own
// update while reporting only that no matching asset existed.
//
// This pins the Makefile's naming to TarballName. It reads the Makefile rather
// than running make, so it works on a Windows checkout where make is absent.
func TestMakefileNamesTarballsTheWayTheUpdaterLooksForThem(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)

	// The line that builds the staging directory name, which becomes the
	// tarball name: name=netrewind-$(VERSION)-$$os-$$arch;
	nameLine := regexp.MustCompile(`name=netrewind-\$\(VERSION\)-\$\$os-\$\$arch`)
	if !nameLine.MatchString(makefile) {
		t.Fatal("the Makefile no longer names release artifacts " +
			"netrewind-$(VERSION)-$os-$arch; if the scheme changed, TarballName " +
			"in release.go has to change with it or updates stop resolving")
	}

	// VERSION must have the leading v stripped, or the name above carries it.
	if !strings.Contains(makefile, "override VERSION := $(patsubst v%,%,$(VERSION))") {
		t.Error("the Makefile no longer strips the leading v from VERSION; " +
			"`git describe` returns v0.8.0 at a tag, and an asset named " +
			"netrewind-v0.8.0-... is one no updater will find")
	}

	// And the two must actually produce the same string. This is the check the
	// two above only support: substitute the way make would, and compare.
	const version = "0.9.0"
	fromMakefile := "netrewind-" + version + "-linux-" + runtime.GOARCH + ".tar.gz"
	if got := TarballName(version); got != fromMakefile {
		t.Errorf("the updater asks for %s; `make release` writes %s", got, fromMakefile)
	}
}

// The appliance image is built by a different script from the tarballs, and it
// used to work out its own version instead of being told one.
//
// That is how 0.8.0 published an appliance that reported itself as "0fb51d7".
// The image was built from an untagged tree, `git describe --tags --always`
// fell back to the commit hash, and the image went out naming itself something
// no operator could match against any release - while the tarballs beside it in
// the same release correctly said 0.8.0.
//
// The consequence is worse than a cosmetic one, which is why this test lives in
// the update package: a bare hash is not a version ParseVersion can read, so
// such an image treats itself as a development build and declines every update
// it is ever offered. On an appliance - the deployment that is meant to be
// plugged in and forgotten - that is a recorder that quietly stops being
// maintained.
func TestTheApplianceImageIsBuiltWithTheReleaseVersion(t *testing.T) {
	root := filepath.Join("..", "..")

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	if !strings.Contains(string(makefile), "build-image.sh --version $(VERSION)") {
		t.Error("`make image` no longer passes --version $(VERSION) to build-image.sh, " +
			"so the image will name itself whatever git describe happens to return")
	}

	script, err := os.ReadFile(filepath.Join(root, "deploy", "appliance", "build-image.sh"))
	if err != nil {
		t.Fatalf("read build-image.sh: %v", err)
	}
	if !strings.Contains(string(script), "--version) VERSION=") {
		t.Error("build-image.sh no longer accepts --version, so the Makefile cannot tell it one")
	}
	if !strings.Contains(string(script), `VERSION="${VERSION#v}"`) {
		t.Error("build-image.sh no longer strips the leading v; an image built from tag " +
			"v0.9.0 would report v0.9.0 while the tarballs report 0.9.0")
	}

	// The reason all of the above matters, stated as a check rather than as a
	// comment: a commit hash is not something the updater can compare.
	if _, err := ParseVersion("0fb51d7"); err == nil {
		t.Error("ParseVersion now accepts a bare commit hash; if that is deliberate, " +
			"the reasoning above needs revisiting")
	}
}

// A release must not be built with a standard library that has known holes in
// it, and on some machines nothing stops that happening.
//
// go.mod names a toolchain rather than raising the go line, so the tree keeps
// building on a distro that pins GOTOOLCHAIN=local with an older Go - Alpine
// does, because upstream toolchains are linked against glibc. The cost is that
// on exactly those machines the directive has no effect: the build succeeds and
// silently links the older standard library. govulncheck reports nine
// vulnerabilities in a tree built that way and none in one built with the
// toolchain go.mod asks for, and one of them is in html/template, which is what
// renders network-supplied strings into the web interface.
//
// Building day to day that way is deliberate and fine. Publishing that way is
// not: the binary goes to other people, and a recorder with update.apply on
// installs it without anybody looking. So the release path has to check.
func TestTheReleaseRefusesAnOlderToolchainThanGoModAsksFor(t *testing.T) {
	root := filepath.Join("..", "..")

	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !regexp.MustCompile(`(?m)^toolchain go1\.\d+`).Match(mod) {
		t.Skip("go.mod names no toolchain, so there is nothing to enforce")
	}

	data, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)

	if !strings.Contains(makefile, "toolchain-check:") {
		t.Fatal("the Makefile has no toolchain-check target; a release built on a " +
			"machine with GOTOOLCHAIN=local and an older Go would ship a standard " +
			"library with known holes, and nothing would say so")
	}
	// It has to read the version out of go.mod rather than carry its own copy,
	// or the two drift and the check starts approving what it exists to refuse.
	if !strings.Contains(makefile, "sed -n 's/^toolchain //p' go.mod") {
		t.Error("toolchain-check no longer reads the wanted version from go.mod")
	}
	for _, target := range []string{"release:", "image:"} {
		line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `.*$`).FindString(makefile)
		if line == "" {
			t.Errorf("no %s target in the Makefile", target)
			continue
		}
		if !strings.Contains(line, "toolchain-check") {
			t.Errorf("%q does not depend on toolchain-check, so it can publish a "+
				"binary built with a standard library go.mod says is too old", line)
		}
	}
}

// build-image.sh is documented as something to run directly, so the guard in
// the Makefile is not enough on its own: `sudo deploy/appliance/build-image.sh`
// never goes near make. An appliance is written to a disk and left running for
// months, which is the worst place to put a standard library with known holes
// in it.
func TestTheApplianceImageRefusesAnOlderToolchainToo(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "deploy", "appliance", "build-image.sh"))
	if err != nil {
		t.Fatalf("read build-image.sh: %v", err)
	}
	text := string(script)

	if !strings.Contains(text, `sed -n 's/^toolchain //p' "$REPO/go.mod"`) {
		t.Fatal("build-image.sh does not read the wanted toolchain from go.mod, " +
			"so running it directly can build an appliance with a standard library " +
			"go.mod says is too old")
	}
	// And it has to refuse rather than warn: this runs unattended.
	guard := text[strings.Index(text, "WANT=$(sed -n"):]
	if end := strings.Index(guard, "\nfi\n"); end > 0 {
		guard = guard[:end]
	}
	if !strings.Contains(guard, "die ") {
		t.Error("the toolchain guard in build-image.sh does not stop the build")
	}
	// Before anything is written to a disk.
	if strings.Index(text, "WANT=$(sed -n") > strings.Index(text, "truncate -s") {
		t.Error("build-image.sh checks the toolchain after it has started creating the disk")
	}
}

// CI must not publish to a release.
//
// It used to. The signature is made on the machine holding the key, so the
// release already carries a signed SHA256SUMS by the time a tag's CI run
// finishes - and action-gh-release replaces assets of the same name, so CI's
// unsigned SHA256SUMS would take the place of the signed one and leave
// SHA256SUMS.sig a signature over a file that is no longer there.
//
// The shape of that failure is why this is a test rather than a comment.
// `sha256sum -c` still passes, because CI's sums match CI's tarballs, so the
// release looks correct to anyone checking it by hand. Meanwhile every recorder
// configured with update.public_key refuses it, and says only that no matching
// asset was found. A release that is wrong in a way that reads as right is
// exactly what this project exists to make impossible.
func TestCIDoesNotPublishOverASignedRelease(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Skipf("no workflow to check: %v", err)
	}
	// Comments are stripped first. The comment beside this in the workflow
	// names action-gh-release in order to explain why it is not used, and a
	// test that cannot tell an explanation from a step is a test that punishes
	// writing the explanation down.
	var live []string
	for _, line := range strings.Split(string(data), "\n") {
		if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, "#") {
			live = append(live, line)
		}
	}
	workflow := strings.Join(live, "\n")

	if strings.Contains(workflow, "action-gh-release") {
		t.Error("CI uses action-gh-release, which replaces assets of the same name. " +
			"A tag build finishing after the release was uploaded would overwrite the " +
			"signed SHA256SUMS with an unsigned one, and SHA256SUMS.sig would then " +
			"verify against nothing while sha256sum -c still passed")
	}
	// contents: write is what publishing needs. Nothing in this workflow should.
	if strings.Contains(workflow, "contents: write") {
		t.Error("a job in CI asks for contents: write; nothing here should be able " +
			"to alter a release")
	}
}
