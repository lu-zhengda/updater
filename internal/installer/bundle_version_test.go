package installer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lu-zhengda/updater/internal/checker"
	"howett.net/plist"
)

func versionPlist(t *testing.T, bundleID, version string, format int) []byte {
	t.Helper()
	data, err := plist.Marshal(map[string]string{"CFBundleIdentifier": bundleID, "CFBundleShortVersionString": version}, format)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReadBundleVersionZIP(t *testing.T) {
	for _, format := range []int{plist.XMLFormat, plist.BinaryFormat} {
		t.Run(fmt.Sprint(format), func(t *testing.T) {
			var buffer bytes.Buffer
			archive := zip.NewWriter(&buffer)
			for name, data := range map[string][]byte{
				"Other.app/Contents/Info.plist":                                       versionPlist(t, "com.other.app", "99.0", format),
				"Release/Renamed.app/Contents/Info.plist":                             versionPlist(t, "com.example.app", "1.1.33", format),
				"Release/Renamed.app/Contents/Helpers/Helper.app/Contents/Info.plist": versionPlist(t, "com.example.app", "99.0", format),
			} {
				entry, err := archive.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(buffer.Bytes()) }))
			defer ts.Close()
			inst := New(&checker.MockCmdRunner{}, ts.Client())
			sum := sha256.Sum256(buffer.Bytes())
			got, err := inst.ReadBundleVersion(context.Background(), ts.URL+"/app.zip", fmt.Sprintf("sha256:%x", sum), "com.example.app")
			if err != nil || got != "1.1.33" {
				t.Fatalf("version = %q, error = %v", got, err)
			}
			if _, err := inst.ReadBundleVersion(context.Background(), ts.URL+"/app.zip", "sha256:wrong", "com.example.app"); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
				t.Fatalf("digest mismatch was not rejected: %v", err)
			}
		})
	}
}

func TestBundleVersionRejectsInvalidMetadata(t *testing.T) {
	valid := versionPlist(t, "com.example.app", "1.1.33", plist.XMLFormat)
	for _, tc := range []struct {
		name      string
		files     fstest.MapFS
		wantError string
	}{
		{"wrong identity", fstest.MapFS{"App.app/Contents/Info.plist": {Data: versionPlist(t, "com.other", "9.0", plist.XMLFormat)}}, "no app"},
		{"missing version", fstest.MapFS{"App.app/Contents/Info.plist": {Data: versionPlist(t, "com.example.app", "", plist.XMLFormat)}}, "missing or empty"},
		{"invalid plist", fstest.MapFS{"App.app/Contents/Info.plist": {Data: []byte("invalid")}}, "invalid Info.plist"},
		{"oversized plist", fstest.MapFS{"App.app/Contents/Info.plist": {Data: bytes.Repeat([]byte("x"), maxBundlePlistSize+1)}}, "at most 1 MiB"},
		{"non-regular file", fstest.MapFS{"App.app/Contents/Info.plist": {Mode: fs.ModeNamedPipe}}, "regular file"},
		{"ambiguous identity", fstest.MapFS{"One.app/Contents/Info.plist": {Data: valid}, "Two.app/Contents/Info.plist": {Data: valid}}, "multiple apps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := bundleVersionInFS(context.Background(), tc.files, "com.example.app"); err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func TestBundleVersionRejectsEscapingSymlink(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "Info.plist")
	if err := os.WriteFile(outside, versionPlist(t, "com.example.app", "99.0", plist.XMLFormat), 0o600); err != nil {
		t.Fatal(err)
	}
	rootDir := t.TempDir()
	contents := filepath.Join(rootDir, "App.app", "Contents")
	if err := os.MkdirAll(contents, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(contents, "Info.plist")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if got, err := bundleVersionInFS(context.Background(), root.FS(), "com.example.app"); err == nil {
		t.Fatalf("read version %q from outside the mounted volume", got)
	}
}

type bundleVersionRunner func(context.Context, string, ...string) ([]byte, error)

func (run bundleVersionRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return run(ctx, name, args...)
}

func TestDMGBundleVersionCleanup(t *testing.T) {
	for _, failDetach := range []bool{false, true} {
		t.Run(fmt.Sprint(failDetach), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mountPoint string
			detaches := 0
			runner := bundleVersionRunner(func(runCtx context.Context, name string, args ...string) ([]byte, error) {
				if name != "hdiutil" {
					t.Fatalf("unexpected command %s", name)
				}
				if args[0] == "attach" {
					if strings.Join(args[1:5], " ") != "-readonly -nobrowse -noautoopen -mountpoint" {
						t.Fatalf("unsafe mount args: %v", args)
					}
					mountPoint = args[5]
					t.Cleanup(func() { os.RemoveAll(mountPoint) })
					cancel()
					return nil, nil
				}
				detaches++
				if runCtx.Err() != nil {
					t.Fatal("cleanup inherited cancellation")
				}
				if failDetach {
					return nil, errors.New("busy volume")
				}
				return nil, nil
			})
			_, err := New(runner, nil).readDMGBundleVersion(ctx, "/unused/test.dmg", "com.example.app")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			_, statErr := os.Stat(mountPoint)
			if failDetach {
				if detaches != 2 || !strings.Contains(err.Error(), "failed to detach") || statErr != nil {
					t.Fatalf("failed detach cleanup: %d, %v, %v", detaches, err, statErr)
				}
			} else if detaches != 1 || !os.IsNotExist(statErr) {
				t.Fatalf("mount directory not cleaned: %d, %v", detaches, statErr)
			}
		})
	}
}

func TestReadBundleVersionDMG(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("DMG inspection requires macOS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	contents := filepath.Join(source, "Example.app", "Contents")
	if err := os.MkdirAll(contents, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), versionPlist(t, "com.example.app", "1.1.33", plist.BinaryFormat), 0o600); err != nil {
		t.Fatal(err)
	}
	dmg := filepath.Join(root, "test.dmg")
	runner := &checker.RealCmdRunner{}
	if _, err := runner.Run(ctx, "hdiutil", "create", "-srcfolder", source, "-format", "UDZO", "-volname", "Updater version test", dmg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dmg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer ts.Close()
	got, err := New(runner, ts.Client()).ReadBundleVersion(ctx, ts.URL+"/test.dmg", "", "com.example.app")
	if err != nil || got != "1.1.33" {
		t.Fatalf("version = %q, error = %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(contents, "Info.plist")); err != nil {
		t.Fatal("inspection changed the source app")
	}
}
