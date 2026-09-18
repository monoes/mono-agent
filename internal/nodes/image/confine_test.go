package image

import (
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/fsconfine"
	"github.com/monoes/mono-agent/internal/workflow"
)

// confinedImageDirs returns a workdir and an outside directory, each holding
// a small PNG, plus a symlink in the workdir pointing outside (C-46).
func confinedImageDirs(t *testing.T) (root, outside string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, outside = filepath.Join(base, "work"), filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(d, "pic.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

func itemWith(path string) workflow.NodeInput {
	return workflow.NodeInput{Items: []workflow.Item{workflow.NewItem(map[string]interface{}{"image_path": path})}}
}

func TestImageNodes_RefuseInputPathsOutsideOrgWorkdir(t *testing.T) {
	root, outside := confinedImageDirs(t)
	ctx := fsconfine.WithRoot(context.Background(), root)
	nodes := map[string]workflow.NodeExecutor{
		"image.info": &ImageInfoNode{}, "image.resize": &ImageResizeNode{}, "image.crop": &ImageCropNode{},
		"image.thumbnail": &ImageThumbnailNode{}, "image.convert": &ImageConvertNode{}, "image.adjust": &ImageAdjustNode{},
	}
	cfg := map[string]interface{}{"width": 2.0, "height": 2.0, "format": "png", "brightness": 10.0, "aspect_ratio": "1:1"}
	for name, n := range nodes {
		for _, p := range []string{filepath.Join(outside, "pic.png"), "../outside/pic.png", "escape/pic.png"} {
			_, err := n.Execute(ctx, itemWith(p), cfg)
			if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
				t.Errorf("%s %q: err = %v, want ErrOutsideWorkdir", name, p, err)
			}
		}
	}
	assertOnlyPic(t, outside)
}

func TestImageNodes_RefuseOutputDirOutsideOrgWorkdir(t *testing.T) {
	root, outside := confinedImageDirs(t)
	ctx := fsconfine.WithRoot(context.Background(), root)
	for _, dir := range []string{outside, "../outside", "escape"} {
		_, err := (&ImageResizeNode{}).Execute(ctx, itemWith("pic.png"), map[string]interface{}{"width": 2.0, "output_dir": dir})
		if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
			t.Errorf("output_dir %q: err = %v, want ErrOutsideWorkdir", dir, err)
		}
	}
	assertOnlyPic(t, outside)

	out, err := (&ImageResizeNode{}).Execute(ctx, itemWith("pic.png"), map[string]interface{}{"width": 2.0, "fit": "width", "output_dir": "thumbs"})
	if err != nil {
		t.Fatalf("resize inside the workdir: %v", err)
	}
	got, _ := out[0].Items[0].JSON["image_path"].(string)
	if !strings.HasPrefix(got, filepath.Join(root, "thumbs")+string(filepath.Separator)) {
		t.Errorf("output %q should land in the workdir's thumbs folder", got)
	}
}

func TestImageVaultSave_RefusesPathsOutsideOrgWorkdir(t *testing.T) {
	root, outside := confinedImageDirs(t)
	ctx := fsconfine.WithRoot(newImageVaultTestCtx(t), root)
	_, err := (&ImageVaultSaveNode{}).Execute(ctx, itemWith(filepath.Join(outside, "pic.png")), map[string]interface{}{})
	if !errors.Is(err, fsconfine.ErrOutsideWorkdir) {
		t.Fatalf("err = %v, want ErrOutsideWorkdir", err)
	}
	if _, err := (&ImageVaultSaveNode{}).Execute(ctx, itemWith("pic.png"), map[string]interface{}{}); err != nil {
		t.Fatalf("vault_save inside the workdir: %v", err)
	}
}

func TestImageInfo_UnconfinedReadsAnyPath(t *testing.T) {
	_, outside := confinedImageDirs(t)
	if _, err := (&ImageInfoNode{}).Execute(context.Background(), itemWith(filepath.Join(outside, "pic.png")), nil); err != nil {
		t.Fatalf("unconfined image.info must behave as before: %v", err)
	}
}

func assertOnlyPic(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "pic.png" {
			t.Errorf("a refused call wrote %s outside the workdir", e.Name())
		}
	}
}
