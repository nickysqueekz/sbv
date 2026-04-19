package internal

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// WatchDirFile represents an XML file found in a watch directory
type WatchDirFile struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Dir     string    `json:"dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// GetWatchDirs returns the list of configured watch directories from WATCH_DIRS env var
func GetWatchDirs() []string {
	val := os.Getenv("WATCH_DIRS")
	if val == "" {
		return nil
	}
	var dirs []string
	for _, d := range strings.Split(val, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// ListWatchDirFiles scans all configured watch directories and returns XML files found
func ListWatchDirFiles() ([]WatchDirFile, error) {
	dirs := GetWatchDirs()
	var files []WatchDirFile

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Skip inaccessible directories but don't fail the whole request
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".xml") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, WatchDirFile{
				Name:    name,
				Path:    filepath.Join(dir, name),
				Dir:     dir,
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
		}
	}

	return files, nil
}

// HandleListWatchDirs returns the list of XML files available in watch directories
func HandleListWatchDirs(c echo.Context) error {
	watchDirs := GetWatchDirs()

	files, err := ListWatchDirFiles()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]interface{}{
			"error": fmt.Sprintf("Failed to list watch directories: %v", err),
		})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"watch_dirs": watchDirs,
		"files":      files,
	})
}

// HandleImportWatchDir queues a file from a watch directory into the current user's ingest directory
func HandleImportWatchDir(dataDir string) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req struct {
			Path string `json:"path"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "Invalid request body",
			})
		}

		if req.Path == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "path is required",
			})
		}

		// Security: ensure the requested path is within one of the configured watch dirs
		watchDirs := GetWatchDirs()
		allowed := false
		for _, dir := range watchDirs {
			absDir, err := filepath.Abs(dir)
			if err != nil {
				continue
			}
			absPath, err := filepath.Abs(req.Path)
			if err != nil {
				continue
			}
			if strings.HasPrefix(absPath, absDir+string(os.PathSeparator)) || absPath == absDir {
				allowed = true
				break
			}
		}
		if !allowed {
			return c.JSON(http.StatusForbidden, map[string]string{
				"error": "Path is not within a configured watch directory",
			})
		}

		// Verify it's an XML file
		if !strings.HasSuffix(strings.ToLower(req.Path), ".xml") {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "Only XML files can be imported",
			})
		}

		// Get user ID from context
		userID, ok := c.Get("user_id").(string)
		if !ok || userID == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "User not authenticated",
			})
		}

		// Ensure ingest directory exists
		ingestDir := filepath.Join(dataDir, userID, "ingest")
		if err := os.MkdirAll(ingestDir, 0755); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("Failed to create ingest directory: %v", err),
			})
		}

		// Copy the file into the user's ingest directory
		filename := filepath.Base(req.Path)
		destPath := filepath.Join(ingestDir, filename)

		src, err := os.Open(req.Path)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("Failed to open source file: %v", err),
			})
		}
		defer src.Close()

		dst, err := os.Create(destPath)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("Failed to create destination file: %v", err),
			})
		}
		defer dst.Close()

		if _, err := io.Copy(dst, src); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("Failed to copy file: %v", err),
			})
		}

		return c.JSON(http.StatusOK, map[string]string{
			"message":  fmt.Sprintf("File '%s' queued for import. It will be processed within 1 minute.", filename),
			"filename": filename,
		})
	}
}
