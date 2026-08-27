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
