package contracts

import (
	"regexp"
	"testing"
)

// releasePattern is a released semantic version: no pre-release and no
// pseudo-version (vX.Y.Z-yyyymmddhhmmss-abcdefabcdef is a pre-release
// in semver terms, so it is refused by the same rule).
var releasePattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// The lab builds against a tagged uspace-core release, not a commit:
// a pseudo-version names a tree no release note describes, and the
// changelog of each lab pull request cites core by its release.
func TestCorePinIsARelease(t *testing.T) {
	_, version, err := ModuleDir(CoreModule)
	if err != nil {
		t.Fatal(err)
	}
	if !releasePattern.MatchString(version) {
		t.Fatalf("%s is pinned at %q, want a released vX.Y.Z tag", CoreModule, version)
	}
}

// The rule above accepts a release and refuses a pseudo-version and a
// pre-release, so a pass of TestCorePinIsARelease means something.
func TestReleasePatternJudgesBothWays(t *testing.T) {
	for _, v := range []string{"v1.6.0", "v1.10.12", "v0.0.1"} {
		if !releasePattern.MatchString(v) {
			t.Errorf("%s refused, want accepted", v)
		}
	}
	for _, v := range []string{
		"v1.5.1-0.20261013183409-770379db7556",
		"v0.0.0-20261013183409-770379db7556",
		"v1.6.0-rc.1",
		"v1.6.0+incompatible",
		"1.6.0",
		"v1.6",
		"v01.6.0",
		"",
	} {
		if releasePattern.MatchString(v) {
			t.Errorf("%q accepted, want refused", v)
		}
	}
}
