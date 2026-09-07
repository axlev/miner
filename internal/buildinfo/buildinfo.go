// Package buildinfo resolves the actual miner build identity from Go's embedded VCS metadata.
package buildinfo

import "runtime/debug"

// MinerVersion returns the Git revision this binary was built from, suffixed with
// "-dirty" when the build tree had uncommitted changes. It returns "unknown" when
// no VCS revision was embedded (for example, a build outside a Git checkout, a
// shallow clone, or `-buildvcs=false`), so provenance fails closed instead of
// reporting a fabricated version.
func MinerVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	revision, dirty := "", false
	found := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
			found = true
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if !found || revision == "" {
		return "unknown"
	}
	if dirty {
		return revision + "-dirty"
	}
	return revision
}
