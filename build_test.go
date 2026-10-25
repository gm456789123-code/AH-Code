package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectBuildCommandProjectTypes(t *testing.T) {
	a := isolate(t)

	t.Run("empty folder", func(t *testing.T) {
		if d := a.DetectBuildCommand(t.TempDir()); d.Found {
			t.Errorf("empty folder must not claim a build system: %+v", d)
		}
	})

	t.Run("go module", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "go.mod", "module x\n")
		d := a.DetectBuildCommand(dir)
		if !d.Found || d.Command != "go build ./..." {
			t.Errorf("%+v", d)
		}
	})

	t.Run("node with build script", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "package.json", `{"scripts":{"build":"vite build","test":"vitest"}}`)
		d := a.DetectBuildCommand(dir)
		if !d.Found || d.Command != "npm run build" {
			t.Errorf("%+v", d)
		}
	})

	t.Run("node without build script falls back", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "package.json", `{"scripts":{"test":"jest"}}`)
		d := a.DetectBuildCommand(dir)
		if !d.Found || d.Command != "npm run test" {
			t.Errorf("must fall back to an existing script, got %+v", d)
		}
	})

	t.Run("node package manager from lockfile", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "package.json", `{"scripts":{"build":"tsc"}}`)
		write(t, dir, "pnpm-lock.yaml", "")
		d := a.DetectBuildCommand(dir)
		if d.Command != "pnpm run build" {
			t.Errorf("%+v", d)
		}
	})

	t.Run("yarn lockfile uses yarn syntax", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "package.json", `{"scripts":{"build":"tsc"}}`)
		write(t, dir, "yarn.lock", "")
		d := a.DetectBuildCommand(dir)
		if d.Command != "yarn build" {
			t.Errorf("%+v", d)
		}
	})

	t.Run("cargo", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "Cargo.toml", "[package]\n")
		d := a.DetectBuildCommand(dir)
		if d.Command != "cargo build" {
			t.Errorf("%+v", d)
		}
	})

	t.Run("maven", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "pom.xml", "<project/>")
		d := a.DetectBuildCommand(dir)
		if d.Command != "mvn -q -DskipTests compile" {
			t.Errorf("%+v", d)
		}
	})

	t.Run("dotnet sln", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "App.sln", "")
		d := a.DetectBuildCommand(dir)
		if d.Command != "dotnet build" {
			t.Errorf("%+v", d)
		}
	})

	t.Run("python", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "requirements.txt", "")
		d := a.DetectBuildCommand(dir)
		if d.Command != "python -m compileall -q ." {
			t.Errorf("%+v", d)
		}
	})

	t.Run("monorepo frontend subfolder", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, filepath.Join("frontend", "package.json"), `{"scripts":{"build":"vite build"}}`)
		d := a.DetectBuildCommand(dir)
		if !d.Found || !strings.Contains(d.Command, "frontend") || !strings.Contains(d.Command, "run build") {
			t.Errorf("%+v", d)
		}
	})

	t.Run("go.mod wins over a stray package.json at root", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "go.mod", "module x\n")
		write(t, dir, "package.json", `{"scripts":{"build":"whatever"}}`)
		d := a.DetectBuildCommand(dir)
		if d.Command != "go build ./..." {
			t.Errorf("go.mod at root should take priority, got %+v", d)
		}
	})

	t.Run("uses currentFolder when no path given", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "Cargo.toml", "")
		a.currentFolder = dir
		defer func() { a.currentFolder = "" }()
		if d := a.DetectBuildCommand(""); d.Command != "cargo build" {
			t.Errorf("%+v", d)
		}
	})

	t.Run("this repository resolves to itself", func(t *testing.T) {
		wd, _ := os.Getwd()
		d := a.DetectBuildCommand(wd)
		if !d.Found || d.Label != "Go + Wails project" || !strings.Contains(d.Command, "go build ./...") || !strings.Contains(d.Command, "frontend") {
			t.Errorf("%+v", d)
		}
	})
}

func TestChainStopsOnFailure(t *testing.T) {
	a := isolate(t)
	dir := t.TempDir()
	write(t, dir, "wails.json", `{}`)
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, filepath.Join("frontend", "package.json"), `{"scripts":{"build":"vite build"}}`)
	d := a.DetectBuildCommand(dir)

	// Simulate the frontend step failing (no node_modules / vite here) and confirm
	// the chained command reports failure rather than silently running `go build` anyway.
	res := a.RunBuildCommand(dir, d.Command)
	if res.Success {
		t.Fatalf("a failing first step must fail the whole chain, got success: %+v", res)
	}
}

func TestDetectBuildCommandRunsOnThisOS(t *testing.T) {
	a := isolate(t)
	dir := t.TempDir()
	write(t, dir, "go.mod", "module smoke\n")
	write(t, dir, "main.go", "package main\nfunc main() {}\n")

	d := a.DetectBuildCommand(dir)
	res := a.RunBuildCommand(dir, d.Command)
	if !res.Success {
		t.Fatalf("go build on a trivial module must succeed: %+v", res)
	}
}

func TestNpmPrefixedUsesRightFlagPerManager(t *testing.T) {
	cases := map[string]string{
		"npm run build":  `npm --prefix "frontend" run build`,
		"pnpm run build": `pnpm --prefix "frontend" run build`,
		"yarn build":     `yarn --cwd "frontend" build`,
	}
	for in, want := range cases {
		if got := npmPrefixed(in, "frontend"); got != want {
			t.Errorf("npmPrefixed(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChainSyntaxMatchesShell(t *testing.T) {
	got := chain("a", "b")
	if runtime.GOOS == "windows" {
		if !strings.Contains(got, "$LASTEXITCODE") {
			t.Errorf("windows chain must check $LASTEXITCODE: %q", got)
		}
	} else if !strings.Contains(got, "&&") {
		t.Errorf("posix chain must use &&: %q", got)
	}
}
