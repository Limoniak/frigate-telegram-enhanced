// Package version holds the version of the build, set at link time:
//
//	go build -ldflags "-X frigate-telegram-enhanced/internal/version.Version=2.2.5"
package version

// Version is the release (e.g. "2.2.5"), followed by the commit when the build
// has changes since (e.g. "2.2.5+a9bed07" on main, "2.2.5+dev.a9bed07" on dev);
// "dev" when nothing set it.
var Version = "dev"
