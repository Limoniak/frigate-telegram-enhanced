// Package version holds the version of the build, set at link time:
//
//	go build -ldflags "-X frigate-telegram-enhanced/internal/version.Version=2.2.5"
package version

// Version is the release (e.g. "2.2.5"), or the branch and commit of a build
// that is not a release (e.g. "main-a9bed07"); "dev" when nothing set it.
var Version = "dev"
