package main

import (
	"strconv"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ── Image Vault ──────────────────────────────────────────────────────────────

// Every image-vault binding shells out to `monoagentcli image …`, which
// owns the vault_images queries and the vault folder. The rows come back in
// the shape these bindings have always returned (snake_case keys, url
// /vault-image/<file>). User-supplied text (paths, labels, queries) goes
// after "--" so a leading "-" is never read as a flag.

func (a *App) GetVaultImages(limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 200
	}
	return a.imageRows("image", "list", "--limit", strconv.Itoa(limit))
}

func (a *App) SearchVaultImages(query string) ([]map[string]interface{}, error) {
	return a.imageRows("image", "search", "--", query)
}

func (a *App) imageRows(args ...string) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	if err := a.runMonoCLI("", &out, args...); err != nil {
		return nil, err
	}
	if out == nil {
		out = []map[string]interface{}{}
	}
	return out, nil
}

func (a *App) GetVaultImage(id string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := a.runMonoCLI("", &out, "image", "get", "--", id); err != nil {
		return nil, err
	}
	return out, nil
}

// GetVaultImageData returns a vault image as a base64 data URL (e.g.
// "data:image/png;base64,..."). This is the reliable way to display vault
// images inside the Wails WebView without relying on the HTTP asset handler.
func (a *App) GetVaultImageData(id string) (string, error) {
	var out struct {
		DataURL string `json:"data_url"`
	}
	if err := a.runMonoCLI("", &out, "image", "data", "--", id); err != nil {
		return "", err
	}
	return out.DataURL, nil
}

func (a *App) AddVaultImage(srcPath, label string) (map[string]interface{}, error) {
	args := []string{"image", "add"}
	if label != "" {
		args = append(args, "--label", label)
	}
	var out map[string]interface{}
	if err := a.runMonoCLI("", &out, append(args, "--", srcPath)...); err != nil {
		return nil, err
	}
	return out, nil
}

// OpenVaultFilePicker opens a native file picker and returns the selected file path (empty if cancelled).
func (a *App) OpenVaultFilePicker() string {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Image",
		Filters: []runtime.FileFilter{
			{DisplayName: "Images", Pattern: "*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp"},
		},
	})
	if err != nil {
		return ""
	}
	return path
}

// SaveVaultImageToFile opens a native Save File dialog pre-filled with suggestedName,
// then copies the vault image file to the chosen path. Returns "" if the user cancels.
func (a *App) SaveVaultImageToFile(id, suggestedName string) string {
	// Check first, so an unknown id fails before the dialog is shown.
	if _, err := a.GetVaultImage(id); err != nil {
		return "error: image not found"
	}
	dest, err := saveImageDialog(a, suggestedName)
	if err != nil || dest == "" {
		return ""
	}
	var out struct {
		Path string `json:"path"`
	}
	if err := a.runMonoCLI("", &out, "image", "export", "--", id, dest); err != nil {
		return "error: " + err.Error()
	}
	return out.Path
}

// saveImageDialog is the native save dialog, a variable so tests can
// answer it.
var saveImageDialog = func(a *App, suggestedName string) (string, error) {
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save Image",
		DefaultFilename: suggestedName,
		Filters: []runtime.FileFilter{
			{DisplayName: "Images", Pattern: "*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp;*.tif"},
		},
	})
}

// UpdateVaultImageLabel renames an image; an empty label clears it.
func (a *App) UpdateVaultImageLabel(id, label string) error {
	args := []string{"image", "label", "--", id}
	if label != "" {
		args = append(args, label)
	}
	return a.runMonoCLI("", nil, args...)
}

func (a *App) DeleteVaultImage(id string) error {
	return a.runMonoCLI("", nil, "image", "delete", "--", id)
}

func (a *App) GetVaultStats() (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := a.runMonoCLI("", &out, "image", "stats"); err != nil {
		return nil, err
	}
	return out, nil
}
