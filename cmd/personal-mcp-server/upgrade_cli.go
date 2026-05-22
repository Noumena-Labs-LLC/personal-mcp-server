package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type upgradeOptions struct {
	ArtifactPath   string
	SHAPath        string
	BinaryPath     string
	RestartService bool
	DryRun         bool
}

func upgradeCommand(args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCommandHelp("upgrade")
		return
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		if !printCommandHelp("upgrade " + args[0]) {
			printCommandHelp("upgrade")
		}
		return
	}
	if args[0] != "local" {
		usage()
		os.Exit(2)
	}
	fs := flagSet("upgrade local")
	shaPath := fs.String("sha256", "", "optional sha256sum file; defaults to ARTIFACT.tar.gz.sha256 when present")
	binary := fs.String("binary", defaultBinaryPath(), "installed personal-mcp-server binary path")
	dryRun := fs.Bool("dry-run", false, "verify, inspect, and build the local artifact without replacing the installed binary")
	noRestart := fs.Bool("no-restart-service", false, "do not restart an installed user service after replacing the binary")
	_ = fs.Parse(args[1:])
	remaining := fs.Args()
	if len(remaining) != 1 {
		log.Fatal("upgrade local requires exactly one artifact tarball")
	}
	opts := upgradeOptions{
		ArtifactPath:   remaining[0],
		SHAPath:        *shaPath,
		BinaryPath:     *binary,
		RestartService: !*noRestart,
		DryRun:         *dryRun,
	}
	if err := upgradeLocal(opts); err != nil {
		log.Fatal(err)
	}
}

func upgradeLocal(opts upgradeOptions) error {
	artifactPath := expandUserPath(opts.ArtifactPath)
	binaryPath := expandUserPath(opts.BinaryPath)
	if err := verifyArtifactSHA256(artifactPath, opts.SHAPath); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp("", "personal-mcp-server-upgrade-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	if err := extractTarGz(artifactPath, tmpDir); err != nil {
		return err
	}
	srcDir, err := findExtractedModuleRoot(tmpDir)
	if err != nil {
		return err
	}
	if err := requireUpgradeModule(srcDir); err != nil {
		return err
	}
	artifactVersion, err := upgradeArtifactVersion(srcDir)
	if err != nil {
		return err
	}
	serviceWasInstalled := userServiceManifestExists()
	fmt.Printf("artifact: %s\n", artifactPath)
	fmt.Printf("artifact version: %s\n", artifactVersion)
	fmt.Println("module: github.com/noumena-labs-llc/personal-mcp-server")
	fmt.Printf("target binary: %s\n", binaryPath)
	fmt.Printf("service installed: %t\n", serviceWasInstalled)
	fmt.Printf("restart service: %t\n", opts.RestartService && serviceWasInstalled)

	builtBinary := filepath.Join(tmpDir, "personal-mcp-server-built")
	if err := runUpgradeCommand(srcDir, "go", "build", "-o", builtBinary, "./cmd/personal-mcp-server"); err != nil {
		return err
	}
	if opts.DryRun {
		fmt.Println("dry run: built artifact successfully; installed binary was not changed")
		return nil
	}
	if opts.RestartService && serviceWasInstalled {
		if err := serviceStop(); err != nil {
			fmt.Fprintf(os.Stderr, "service stop warning: %v\n", err)
		}
	}
	replacement, err := replaceExecutableWithRollback(builtBinary, binaryPath)
	if err != nil {
		return err
	}
	fmt.Printf("upgraded binary: %s\n", binaryPath)
	if err := runInstalledVersion(binaryPath); err != nil {
		replacement.restore()
		return err
	}
	if opts.RestartService && serviceWasInstalled {
		if err := serviceStart(); err != nil {
			replacement.restore()
			if restoreErr := serviceStart(); restoreErr != nil {
				return fmt.Errorf("service restart failed after upgrade (%w); restored previous binary but restart also failed: %w", err, restoreErr)
			}
			return fmt.Errorf("service restart failed after upgrade (%w); restored previous binary and restarted service", err)
		}
	}
	replacement.cleanup()
	return nil
}

func verifyArtifactSHA256(artifactPath, shaPath string) error {
	if shaPath == "" {
		candidate := artifactPath + ".sha256"
		if _, err := os.Stat(candidate); err == nil {
			shaPath = candidate
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if shaPath == "" {
		fmt.Fprintln(os.Stderr, "sha256 warning: no .sha256 file found; continuing without checksum verification")
		return nil
	}
	shaPath = expandUserPath(shaPath)
	expected, err := readSHA256SumFile(shaPath)
	if err != nil {
		return err
	}
	actual, err := fileSHA256Hex(artifactPath)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("sha256 mismatch for %s: got %s, want %s", artifactPath, actual, expected)
	}
	fmt.Printf("verified sha256: %s\n", shaPath)
	return nil
}

func readSHA256SumFile(path string) (string, error) {
	body, err := os.ReadFile(path) //nolint:gosec // user-supplied local checksum path for explicit local upgrade.
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty sha256 file: %s", path)
	}
	digest := strings.ToLower(fields[0])
	if len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("invalid sha256 digest in %s", path)
	}
	for _, ch := range digest {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return "", fmt.Errorf("invalid sha256 digest in %s", path)
		}
	}
	return digest, nil
}

func fileSHA256Hex(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // user-supplied local artifact path for explicit local upgrade.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func extractTarGz(artifactPath, destDir string) error {
	f, err := os.Open(artifactPath) //nolint:gosec // user-supplied local artifact path for explicit local upgrade.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := extractTarEntry(tr, header, destDir); err != nil {
			return err
		}
	}
}

func extractTarEntry(r io.Reader, header *tar.Header, destDir string) error {
	cleanName := filepath.Clean(header.Name)
	if cleanName == "." || filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) || cleanName == ".." {
		return fmt.Errorf("unsafe tar entry path: %s", header.Name)
	}
	target := filepath.Join(destDir, cleanName)
	if !strings.HasPrefix(target, filepath.Clean(destDir)+string(filepath.Separator)) {
		return fmt.Errorf("unsafe tar entry target: %s", header.Name)
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o750)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, header.FileInfo().Mode().Perm()) //nolint:gosec // target path is constrained under the temp extraction directory.
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, r); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	case tar.TypeSymlink, tar.TypeLink:
		return fmt.Errorf("refusing link entry in upgrade artifact: %s", header.Name)
	default:
		return nil
	}
}

func findExtractedModuleRoot(tmpDir string) (string, error) {
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(tmpDir, entry.Name())
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("upgrade artifact does not contain a top-level Go module")
}

func requireUpgradeModule(srcDir string) error {
	body, err := os.ReadFile(filepath.Join(srcDir, "go.mod")) //nolint:gosec // srcDir is a safely extracted local artifact directory.
	if err != nil {
		return err
	}
	firstLine := strings.TrimSpace(strings.SplitN(string(body), "\n", 2)[0])
	if firstLine != "module github.com/noumena-labs-llc/personal-mcp-server" {
		return fmt.Errorf("upgrade artifact module = %q, want github.com/noumena-labs-llc/personal-mcp-server", firstLine)
	}
	return nil
}

func upgradeArtifactVersion(srcDir string) (string, error) {
	versionBody, err := os.ReadFile(filepath.Join(srcDir, "VERSION")) //nolint:gosec // srcDir is a safely extracted local artifact directory.
	if err != nil {
		return "", err
	}
	artifactVersion := strings.TrimSpace(string(versionBody))
	if artifactVersion == "" {
		return "", fmt.Errorf("upgrade artifact VERSION is empty")
	}
	mainBody, err := os.ReadFile(filepath.Join(srcDir, "cmd", "personal-mcp-server", "main.go")) //nolint:gosec // srcDir is a safely extracted local artifact directory.
	if err != nil {
		return "", err
	}
	constLine := `const version = "` + artifactVersion + `"`
	if !strings.Contains(string(mainBody), constLine) {
		return "", fmt.Errorf("upgrade artifact version mismatch: VERSION=%s but main.go does not contain %s", artifactVersion, constLine)
	}
	return artifactVersion, nil
}

func runUpgradeCommand(dir, name string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // fixed upgrade build command, args are constants.
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runInstalledVersion(binaryPath string) error {
	cmd := exec.CommandContext(context.Background(), binaryPath, "version") //nolint:gosec // binary path is explicit local upgrade destination.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func userServiceManifestExists() bool {
	switch runtime.GOOS {
	case "darwin":
		path, err := launchAgentPath()
		if err != nil {
			return false
		}
		_, err = os.Stat(path)
		return err == nil
	case "linux":
		path, err := systemdUserUnitPath()
		if err != nil {
			return false
		}
		_, err = os.Stat(path)
		return err == nil
	default:
		return false
	}
}

type executableReplacement struct {
	target    string
	backup    string
	hadBackup bool
}

func replaceExecutableWithRollback(source, target string) (executableReplacement, error) {
	replacement := executableReplacement{target: target}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return replacement, err
	}
	if _, err := os.Stat(target); err == nil {
		replacement.hadBackup = true
		replacement.backup = target + ".backup-" + time.Now().UTC().Format("20060102T150405Z")
		if err := copyExecutable(target, replacement.backup); err != nil {
			return replacement, err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return replacement, err
	}
	tmpTarget := target + ".tmp-" + time.Now().UTC().Format("20060102T150405Z")
	if err := copyExecutable(source, tmpTarget); err != nil {
		return replacement, err
	}
	if err := os.Rename(tmpTarget, target); err != nil {
		_ = os.Remove(tmpTarget)
		replacement.cleanup()
		return replacement, err
	}
	return replacement, nil
}

func (r executableReplacement) restore() {
	if !r.hadBackup {
		if err := os.Remove(r.target); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "rollback warning: failed to remove new binary: %v\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "removed new binary after failed upgrade: %s\n", r.target)
		return
	}
	if err := copyExecutable(r.backup, r.target); err != nil {
		fmt.Fprintf(os.Stderr, "rollback warning: failed to restore previous binary: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "restored previous binary: %s\n", r.target)
}

func (r executableReplacement) cleanup() {
	if r.hadBackup {
		_ = os.Remove(r.backup)
	}
}
