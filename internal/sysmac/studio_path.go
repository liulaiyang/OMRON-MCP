package sysmac

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// StudioInstallEnv names the environment variable that points at the Sysmac
// Studio installation directory (the folder containing SysmacStudio.exe).
const StudioInstallEnv = "OMRON_SYSMAC_STUDIO"

var studioInstallCandidates = []string{
	`C:\Program Files\OMRON\Sysmac Studio`,
	`C:\Program Files (x86)\OMRON\Sysmac Studio`,
	`D:\Sysmac Studio`,
	`D:\OMRON\Sysmac Studio`,
	`D:\Program Files\OMRON\Sysmac Studio`,
	`E:\Sysmac Studio`,
	`C:\OMRON\Sysmac Studio`,
}

// isStudioInstall reports whether dir looks like a Sysmac Studio installation
// usable for headless builds.
func isStudioInstall(dir string) bool {
	if dir == "" {
		return false
	}
	for _, rel := range []string{
		filepath.Join("Modules", "Nex", "INexBuilder2.dll"),
		filepath.Join("builder2", "NexBuilder2.dll"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			return false
		}
	}
	return true
}

// ResolveStudioInstall returns the Sysmac Studio installation directory:
// the explicit argument if given, then $OMRON_SYSMAC_STUDIO, then the first
// known install location that contains the Nex builder. Empty if none found.
func ResolveStudioInstall(explicit string) string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return filepath.Clean(explicit)
	}
	if env := strings.TrimSpace(os.Getenv(StudioInstallEnv)); env != "" {
		return filepath.Clean(env)
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, candidate := range studioInstallCandidates {
		if isStudioInstall(candidate) {
			return candidate
		}
	}
	return ""
}
