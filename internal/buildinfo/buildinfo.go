package buildinfo

import (
	"runtime/debug"
	"strings"
)

const defaultVersion = "dev"

// Version is the human-readable application release version embedded in binaries.
//
// Release builds can override this with:
//
//	-ldflags "-X aws-billing-simulator/internal/buildinfo.Version=<version>"
var Version = defaultVersion

// Info describes the release identity compiled into the running binary.
type Info struct {
	Version   string
	GoVersion string
}

// Current returns normalized build metadata for the running binary.
func Current() Info {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = defaultVersion
	}

	info := Info{Version: version}
	if build, ok := debug.ReadBuildInfo(); ok {
		info.GoVersion = build.GoVersion
	}
	return info
}
