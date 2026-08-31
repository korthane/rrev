package main

import "testing"

// TestBuildVersion covers the order the version is resolved in: a stamped-in
// version is what the release build sets, the module version is what a
// `go install ...@tag` binary carries instead, and the placeholder is all a
// build from a working tree can honestly claim.
func TestBuildVersion(t *testing.T) {
	tests := []struct {
		name            string
		stamped, module string
		want            string
	}{
		{"stamped wins over the module version", "v0.2.0", "v0.1.0", "v0.2.0"},
		{"stamped alone", "v0.2.0", "", "v0.2.0"},
		{"module version when nothing was stamped in", devVersion, "v0.1.0", "v0.1.0"},
		{"module version when the stamp was cleared", "", "v0.1.0", "v0.1.0"},
		// An untagged `go build` records this, and it names the commit exactly.
		{"a pseudo-version identifies the commit", devVersion, "v0.0.0-20260831142701-881d92c9ff8a+dirty", "v0.0.0-20260831142701-881d92c9ff8a+dirty"},
		{"neither: no module version", devVersion, "", devVersion},
		{"neither: an untagged build", devVersion, "(devel)", devVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildVersion(tt.stamped, tt.module); got != tt.want {
				t.Errorf("buildVersion(%q, %q) = %q, want %q", tt.stamped, tt.module, got, tt.want)
			}
		})
	}
}

// TestModuleVersionIsNotAPlaceholder guards the one thing the resolver cannot
// check for itself: that the build information is read from the main module,
// not from a dependency or from the placeholder Go writes for an untagged
// build. A test binary carries no release tag, so anything else here would be
// a version reported to users that no tag corresponds to.
func TestModuleVersionIsNotAPlaceholder(t *testing.T) {
	if got := moduleVersion(); buildVersion(devVersion, got) != devVersion {
		t.Errorf("moduleVersion() = %q, which a test binary would report as a release", got)
	}
}
