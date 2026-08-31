package main

import "runtime/debug"

// devVersion is what a build with no version to report calls itself.
const devVersion = "dev"

// version is overridden at build time via -ldflags "-X main.version=...".
var version = devVersion

// buildVersion reports what --version prints. A version stamped in at build
// time wins; otherwise the module version Go records, which is the release tag
// for a binary installed with `go install ...@v1.2.3`.
func buildVersion(stamped, module string) string {
	if stamped != "" && stamped != devVersion {
		return stamped
	}
	// Go writes "(devel)" when the build had no tag to name.
	if module != "" && module != "(devel)" {
		return module
	}
	return devVersion
}

// moduleVersion reports the version Go recorded for the main module, empty when
// the binary carries no build information at all.
func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return info.Main.Version
}
