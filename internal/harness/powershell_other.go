//go:build !windows

package harness

// A handler installed off Windows embeds an executable path for that platform,
// so omit commandWindows rather than persisting a cwd-searchable launcher into
// a config home that could later be shared with native Windows.
func powershellExecutable() (string, bool) { return "", false }
