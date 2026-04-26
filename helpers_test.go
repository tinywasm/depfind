package depfind

import "testing"

// logf prints only when the test fails or is executed with -v.
// Use instead of t.Logf for internal diagnostic logs.
func logf(t *testing.T, format string, args ...any) {
	t.Helper()
	if testing.Verbose() {
		t.Logf(format, args...)
	}
}
