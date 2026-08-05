package transport

import "testing"

// TestHasBatchExtension covers the path spellings Win32 normalizes before it
// opens a file. Each case is a way to name a .bat/.cmd script that a naive
// "check the final extension" test would miss. See isWindowsBatchCLI.
func TestHasBatchExtension(t *testing.T) {
	batch := []string{
		`claude.cmd`,
		`claude.bat`,
		`C:\npm\claude.cmd`,
		`C:/npm/claude.CMD`,  // extension case is not significant
		`claude.cmd.`,        // Windows strips trailing dots
		`claude.cmd   `,      // and trailing spaces
		`claude.cmd:stream`,  // NTFS stream spec: opens claude.cmd
		`claude:evil.cmd`,    // last-dot scan spans the stream spec
		`C:claude.cmd`,       // drive-relative
		`.cmd`,               // bare extension is a batch name to Win32
		`C:\a.cmd\..\x.exe`,  // a batch-named component anywhere
		`C:\npm\\claude.cmd`, // repeated separators
	}
	for _, p := range batch {
		if !hasBatchExtension(p) {
			t.Errorf("hasBatchExtension(%q) = false, want true", p)
		}
	}

	safe := []string{
		`claude.exe`,
		`C:\Program Files\claude\claude.exe`,
		`/usr/local/bin/claude`,
		`claude`,
		`claude.cmdx`,      // not a batch extension
		`cmd`,              // no dot
		`claude.bats`,      // not .bat
		`batch/claude.exe`, // directory merely named like one
	}
	for _, p := range safe {
		if hasBatchExtension(p) {
			t.Errorf("hasBatchExtension(%q) = true, want false", p)
		}
	}
}
