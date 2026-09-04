package main

import (
	"embed"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// Version is injected at build time via ldflags.
// Falls back to git describe at runtime when built without ldflags (e.g. wails dev).
var (
	version   = ""
	buildDate = ""
)

func init() {
	if version == "" {
		if out, err := exec.Command("git", "describe", "--tags", "--always").Output(); err == nil {
			version = strings.TrimSpace(string(out))
		} else {
			version = "dev"
		}
	}
	if buildDate == "" {
		buildDate = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
}

// enableDevTools controls whether the WebKit inspector opens on startup.
// Set to true temporarily while debugging frontend issues.
const enableDevTools = false

//go:embed all:frontend/dist
var assets embed.FS

// appIcon is the window/taskbar icon for Linux. macOS takes its icon from the
// .app bundle and Windows from the embedded resource, so neither needs this.
//
//go:embed build/appicon.png
var appIcon []byte

// vaultImageHandler serves files from ~/.monoagent/vault/ at /vault-image/<filename>,
// scoped to the currently active profile so one profile cannot enumerate or view
// another profile's vault images.
func vaultImageHandler(app *App) http.Handler {
	// os.UserHomeDir (not $HOME) so the vault dir resolves on Windows too.
	home, _ := os.UserHomeDir()
	vaultDir := filepath.Join(home, ".monoagent", "vault")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/vault-image/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Use filepath.Base to prevent path traversal.
		name := filepath.Base(r.URL.Path)

		if app.db == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		profileID := app.getActiveProfileID()
		if profileID == "" {
			profileID = "default"
		}
		var exists int
		err := app.db.QueryRow(
			`SELECT 1 FROM vault_images WHERE filename = ? AND profile_id = ?`,
			name, profileID,
		).Scan(&exists)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		http.ServeFile(w, r, filepath.Join(vaultDir, name))
	})
}

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:            "Mono Agent",
		Width:            1440,
		Height:           900,
		MinWidth:         1100,
		MinHeight:        700,
		BackgroundColour: &options.RGBA{R: 4, G: 6, B: 10, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: vaultImageHandler(app),
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind:       []interface{}{app},
		Debug: options.Debug{
			OpenInspectorOnStartup: enableDevTools,
		},
		Linux: &linux.Options{
			// Wails only calls gtk_window_set_icon when options.Linux is
			// non-nil (frontend/desktop/linux/window.go), so without this
			// block the window and its dock/taskbar entry have no icon.
			Icon: appIcon,

			// A desktop entry is matched to its window by StartupWMClass,
			// which GTK derives from g_get_prgname() — by default the
			// executable's basename. Ours differs per build path
			// (monoagent-ui from wails.json, MonoAgent from the Makefile,
			// MonoAgent-linux-amd64 from the release tarball), so pin it
			// here and one .desktop file matches however it was installed.
			ProgramName: "monoagent",

			// Wails forces this to Never whenever options.Linux is nil, as a
			// workaround for wailsapp/wails#2977 (blank/garbled webview under
			// some GPU drivers). Populating the struct at all opts out of that
			// default, so restate it rather than silently switching to
			// OnDemand — the zero value — as a side effect of setting an icon.
			WebviewGpuPolicy: linux.WebviewGpuPolicyNever,
		},
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: true,
				HideTitle:                  true,
				FullSizeContent:            true,
			},
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
