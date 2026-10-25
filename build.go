package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// BuildDetection is what DetectBuildCommand found for a project folder.
// It only inspects files; it never runs anything, so the frontend can show
// the chosen command to the user before RunBuildCommand actually executes it.
type BuildDetection struct {
	Found   bool   `json:"found"`
	Command string `json:"command"`
	Label   string `json:"label"` // human description, e.g. "Go + Wails project"
}

// DetectBuildCommand inspects a project folder and picks a one-shot build/compile-check command
// for the most common project layouts. The command is shaped for the shell RunBuildCommand uses
// on this OS (PowerShell on Windows, bash elsewhere), so multi-step builds stop on the first failure.
func (a *App) DetectBuildCommand(folderPath string) BuildDetection {
	dir := folderPath
	if dir == "" {
		dir = a.currentFolder
	}
	if dir == "" {
		return BuildDetection{}
	}
	has := func(rel ...string) bool {
		_, err := os.Stat(filepath.Join(append([]string{dir}, rel...)...))
		return err == nil
	}

	// Wails app (this project's own shape): Go backend + an npm-built frontend/ workspace
	if has("wails.json") && has("go.mod") {
		frontendDir := "frontend"
		if b, err := os.ReadFile(filepath.Join(dir, "wails.json")); err == nil {
			var cfg struct {
				FrontendDir string `json:"frontend:dir"`
			}
			if json.Unmarshal(b, &cfg) == nil && cfg.FrontendDir != "" {
				frontendDir = cfg.FrontendDir
			}
		}
		if has(frontendDir, "package.json") {
			fe, _ := npmBuildCommand(filepath.Join(dir, frontendDir))
			return BuildDetection{
				Found:   true,
				Command: chain(npmPrefixed(fe, frontendDir), "go build ./..."),
				Label:   "Go + Wails project",
			}
		}
		return BuildDetection{Found: true, Command: "go build ./...", Label: "Go + Wails project"}
	}

	if has("go.mod") {
		return BuildDetection{Found: true, Command: "go build ./...", Label: "Go module"}
	}

	if has("package.json") {
		cmd, mgr := npmBuildCommand(dir)
		return BuildDetection{Found: true, Command: cmd, Label: "Node project (" + mgr + ")"}
	}

	if has("Cargo.toml") {
		return BuildDetection{Found: true, Command: "cargo build", Label: "Rust (Cargo)"}
	}

	if has("pom.xml") {
		return BuildDetection{Found: true, Command: "mvn -q -DskipTests compile", Label: "Java (Maven)"}
	}
	if has("build.gradle") || has("build.gradle.kts") {
		wrapper := "gradle"
		if runtime.GOOS == "windows" && has("gradlew.bat") {
			wrapper = ".\\gradlew.bat"
		} else if runtime.GOOS != "windows" && has("gradlew") {
			wrapper = "./gradlew"
		}
		return BuildDetection{Found: true, Command: wrapper + " build -x test", Label: "Java (Gradle)"}
	}

	if hasGlob(dir, "*.sln") || hasGlob(dir, "*.csproj") {
		return BuildDetection{Found: true, Command: "dotnet build", Label: ".NET project"}
	}

	if has("pyproject.toml") || has("requirements.txt") || has("setup.py") {
		return BuildDetection{Found: true, Command: "python -m compileall -q .", Label: "Python project"}
	}

	// Common layout when the root itself has no manifest: a frontend/ subfolder does
	if has("frontend", "package.json") {
		cmd, mgr := npmBuildCommand(filepath.Join(dir, "frontend"))
		return BuildDetection{Found: true, Command: npmPrefixed(cmd, "frontend"), Label: "Node project (" + mgr + ", in frontend/)"}
	}

	return BuildDetection{}
}

// npmBuildCommand picks the package manager (from the lockfile) and the best available script,
// preferring "build" and falling back to something that still exercises the code.
func npmBuildCommand(dir string) (cmd string, manager string) {
	manager = "npm"
	switch {
	case fileExists(filepath.Join(dir, "pnpm-lock.yaml")):
		manager = "pnpm"
	case fileExists(filepath.Join(dir, "yarn.lock")):
		manager = "yarn"
	}

	script := "build"
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(b, &pkg) == nil {
			if _, ok := pkg.Scripts["build"]; !ok {
				for _, alt := range []string{"compile", "typecheck", "test"} {
					if _, ok := pkg.Scripts[alt]; ok {
						script = alt
						break
					}
				}
			}
		}
	}

	if manager == "yarn" {
		return "yarn " + script, manager
	}
	return manager + " run " + script, manager
}

// npmPrefixed runs an npm/pnpm/yarn script against a subdirectory without "cd"-ing into it,
// which would otherwise leave the shell in the wrong place for the next chained command.
func npmPrefixed(cmd, subdir string) string {
	switch {
	case len(cmd) > 4 && cmd[:4] == "npm ":
		return "npm --prefix \"" + subdir + "\" " + cmd[4:]
	case len(cmd) > 5 && cmd[:5] == "pnpm ":
		return "pnpm --prefix \"" + subdir + "\" " + cmd[5:]
	default: // yarn has no --prefix; --cwd is the equivalent
		return "yarn --cwd \"" + subdir + "\" " + cmd[len("yarn "):]
	}
}

// chain joins commands so the run stops at the first failure, in the syntax of whichever
// shell RunBuildCommand will execute them with on this OS.
func chain(cmds ...string) string {
	if runtime.GOOS == "windows" {
		out := cmds[0]
		for _, c := range cmds[1:] {
			out += "; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }; " + c
		}
		return out
	}
	out := cmds[0]
	for _, c := range cmds[1:] {
		out += " && " + c
	}
	return out
}

func hasGlob(dir, pattern string) bool {
	m, err := filepath.Glob(filepath.Join(dir, pattern))
	return err == nil && len(m) > 0
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
