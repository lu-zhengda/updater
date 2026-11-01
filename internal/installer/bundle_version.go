package installer

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"howett.net/plist"
)

const maxBundlePlistSize = 1 << 20

// ReadBundleVersion downloads a ZIP or DMG and reads the matching app's version
// without executing, installing, or changing any application.
func (inst *Installer) ReadBundleVersion(ctx context.Context, downloadURL, digest, bundleID string) (string, error) {
	if bundleID == "" {
		return "", fmt.Errorf("bundle version extraction requires a bundle ID")
	}
	tmpDir, err := os.MkdirTemp("", "updater-version-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	localPath, err := inst.download(ctx, downloadURL, tmpDir, digest)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(filepath.Ext(localPath)) {
	case ".zip":
		archive, err := zip.OpenReader(localPath)
		if err != nil {
			return "", fmt.Errorf("failed to open release ZIP: %w", err)
		}
		defer archive.Close()
		return bundleVersionInFS(ctx, archive, bundleID)
	case ".dmg":
		return inst.readDMGBundleVersion(ctx, localPath, bundleID)
	default:
		return "", fmt.Errorf("bundle version extraction supports only ZIP and DMG assets, got %q", filepath.Ext(localPath))
	}
}

func (inst *Installer) readDMGBundleVersion(ctx context.Context, dmgPath, bundleID string) (version string, err error) {
	mountPoint, err := os.MkdirTemp("", "updater-mount-*")
	if err != nil {
		return "", err
	}
	mounted := false
	defer func() {
		if mounted {
			// A canceled check must still detach its read-only volume.
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_, detachErr := inst.runner.Run(cleanupCtx, "hdiutil", "detach", mountPoint, "-quiet")
			if detachErr != nil {
				_, detachErr = inst.runner.Run(cleanupCtx, "hdiutil", "detach", mountPoint, "-force", "-quiet")
			}
			if detachErr != nil {
				err = errors.Join(err, fmt.Errorf("failed to detach inspection volume %s: %w", mountPoint, detachErr))
				return // Never remove a directory while its volume is mounted.
			}
		}
		err = errors.Join(err, os.RemoveAll(mountPoint))
	}()
	if _, err := inst.runner.Run(ctx, "hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen", "-mountpoint", mountPoint, dmgPath); err != nil {
		return "", fmt.Errorf("failed to mount release DMG: %w", err)
	}
	mounted = true
	root, err := os.OpenRoot(mountPoint)
	if err != nil {
		return "", err
	}
	defer root.Close()
	return bundleVersionInFS(ctx, root.FS(), bundleID)
}

func bundleVersionInFS(ctx context.Context, files fs.FS, bundleID string) (string, error) {
	var version string
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if name != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "__MACOSX") {
			return fs.SkipDir
		}
		if !strings.HasSuffix(entry.Name(), ".app") {
			return nil
		}
		candidate, err := readBundlePlist(files, path.Join(name, "Contents", "Info.plist"), bundleID)
		if err != nil {
			return fmt.Errorf("failed to inspect %s: %w", name, err)
		}
		if candidate != "" {
			if version != "" {
				return fmt.Errorf("multiple apps in release asset match bundle ID %q", bundleID)
			}
			version = candidate
		}
		return fs.SkipDir // Nested helpers are not the application being updated.
	})
	if err != nil {
		return "", err
	}
	if version == "" {
		return "", fmt.Errorf("no app in release asset matches bundle ID %q", bundleID)
	}
	return version, nil
}

func readBundlePlist(files fs.FS, name, bundleID string) (string, error) {
	stat, err := fs.Stat(files, name)
	if err != nil {
		return "", err
	}
	if !stat.Mode().IsRegular() || stat.Size() > maxBundlePlistSize {
		return "", fmt.Errorf("Info.plist must be a regular file of at most 1 MiB")
	}
	f, err := files.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBundlePlistSize+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxBundlePlistSize {
		return "", fmt.Errorf("Info.plist exceeds 1 MiB")
	}
	var info struct {
		BundleID string `plist:"CFBundleIdentifier"`
		Version  string `plist:"CFBundleShortVersionString"`
	}
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return "", fmt.Errorf("invalid Info.plist: %w", err)
	}
	if info.BundleID != bundleID {
		return "", nil
	}
	if strings.TrimSpace(info.Version) == "" {
		return "", fmt.Errorf("CFBundleShortVersionString is missing or empty")
	}
	return info.Version, nil
}
