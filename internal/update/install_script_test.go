package update

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// install.sh is the other half of the update story: the updater replaces
// binaries in place, and this is what puts them there in the first place and
// what an operator runs to move from one release to the next by hand.
//
// It is shell, so nothing type-checks it, and the two properties below are the
// ones whose failure is silent. Reading the script is worth more than trusting
// it, and this test reads the script.
func TestTheInstallerDoesNotEditAConfigurationSomebodyTuned(t *testing.T) {
	script := readInstaller(t)

	// The rewrite that points db: and rules: at this host's paths belongs to a
	// fresh install only. It used to run unconditionally, so an upgrade moved
	// the store of an operator who had put it on a bigger disk back to the
	// default, the recorder started an empty one there, and months of timeline
	// read as though the network had never done anything. That is the exact
	// confusion this project exists to remove, arriving as a side effect of
	// installing a fix.
	rewrite := regexp.MustCompile(`sed -i "s\|\^db: `)
	loc := rewrite.FindStringIndex(script)
	if loc == nil {
		t.Fatal("install.sh no longer rewrites db: at all; if that is deliberate, " +
			"a fresh install now needs the operator to set the path by hand")
	}
	keeping := strings.Index(script, "keeping existing netrewindd.yaml")
	if keeping < 0 {
		t.Fatal("install.sh no longer reports keeping an existing configuration")
	}
	if loc[0] < keeping {
		t.Fatal("install.sh rewrites db: before deciding whether a configuration " +
			"already exists, so an upgrade edits the operator's file")
	}
	// And it has to be inside the branch that installs a new file: between the
	// else that begins it and the fi that ends it.
	elseAt := strings.Index(script[keeping:], "\nelse\n")
	fiAt := strings.Index(script[keeping:], "\nfi\n")
	switch {
	case elseAt < 0 || fiAt < 0:
		t.Fatal("cannot find the branch that installs a fresh configuration")
	case loc[0]-keeping < elseAt:
		t.Error("the db: rewrite runs in the branch that keeps an existing configuration")
	case loc[0]-keeping > fiAt:
		t.Error("the db: rewrite runs after the branch, so it applies to every install")
	}
}

// CONFDIR was accepted and then ignored: the recorder reads
// /etc/netrewind/netrewindd.yaml unless told otherwise, so a non-default
// CONFDIR installed a configuration nothing would ever read, and the check that
// is meant to catch an unusable configuration checked a different file.
func TestTheInstallerTellsTheRecorderWhereTheConfigurationIs(t *testing.T) {
	script := readInstaller(t)

	if !strings.Contains(script, `CONFIG_FLAG=" --config $CONFDIR/netrewindd.yaml"`) {
		t.Error("install.sh does not pass --config when CONFDIR is not the default")
	}
	for _, want := range []string{
		`"$PREFIX/netrewindd" --check-config $CONFIG_FLAG`,
		`ExecStart=$PREFIX/netrewindd$CONFIG_FLAG`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install.sh does not use CONFIG_FLAG in %q", want)
		}
	}
}

// The directory the binaries go into has to be created, not assumed.
// /usr/local/bin exists on most systems, which is why this went unnoticed; on
// the ones where it does not, the install stopped part way through.
func TestTheInstallerCreatesThePlaceItInstallsInto(t *testing.T) {
	script := readInstaller(t)

	mkdir := strings.Index(script, `install -d -m 0755 "$PREFIX"`)
	if mkdir < 0 {
		t.Fatal("install.sh does not create $PREFIX before installing into it")
	}
	// Only the install path matters. uninstall removes from $PREFIX and runs
	// earlier in the file, which is not the same thing at all.
	install := strings.Index(script, `install -m 0755 "$HERE/netrewindd"`)
	if install < 0 {
		t.Fatal("install.sh no longer installs netrewindd")
	}
	if install < mkdir {
		t.Error("install.sh writes into $PREFIX before creating it")
	}
}

// --uninstall must never touch the store. It is the record; a script that
// deleted evidence as a side effect of removing a program would be
// indefensible, and this is cheap to keep true.
func TestUninstallNeverTouchesTheRecord(t *testing.T) {
	script := readInstaller(t)

	start := strings.Index(script, "uninstall() {")
	end := strings.Index(script[start:], "\n}\n")
	if start < 0 || end < 0 {
		t.Fatal("cannot find the uninstall function in install.sh")
	}
	body := script[start : start+end]
	for _, forbidden := range []string{"$STATEDIR/", "rm -rf \"$STATEDIR", "events.db"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("uninstall mentions %q; it must not remove anything under the state directory", forbidden)
		}
	}
}

func readInstaller(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	return string(data)
}
