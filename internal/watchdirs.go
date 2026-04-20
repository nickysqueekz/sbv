package internal

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// WatchDirSummary summarises the contents of one watch directory
type WatchDirSummary struct {
	Dir        string `json:"dir"`
	TotalFiles int    `json:"total_files"`
	TotalSize  int64  `json:"total_size"`
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

// listXMLFiles returns all XML files found under dir (top-level only)
func listXMLFiles(dir string) ([]WatchDirFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []WatchDirFile
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
	return files, nil
}

// HandleListWatchDirs returns summary counts for each watch directory (not all file paths)
func HandleListWatchDirs(c echo.Context) error {
	watchDirs := GetWatchDirs()
	var summaries []WatchDirSummary

	for _, dir := range watchDirs {
		files, err := listXMLFiles(dir)
		if err != nil {
			// Directory inaccessible — still include it with zero counts
			summaries = append(summaries, WatchDirSummary{Dir: dir})
			continue
		}
		var totalSize int64
		for _, f := range files {
			totalSize += f.Size
		}
		summaries = append(summaries, WatchDirSummary{
			Dir:        dir,
			TotalFiles: len(files),
			TotalSize:  totalSize,
		})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"watch_dirs": summaries,
	})
}

// HandleBrowseWatchDir returns a paginated, searchable list of XML files in a single watch directory.
// Query params: dir (required), page (default 1), per_page (default 25, max 100), search (substring filter on filename)
func HandleBrowseWatchDir(c echo.Context) error {
	dir := c.QueryParam("dir")
	if dir == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "dir is required"})
	}

	// Security: dir must be one of the configured watch dirs
	watchDirs := GetWatchDirs()
	allowed := false
	for _, wd := range watchDirs {
		absWd, _ := filepath.Abs(wd)
		absDir, _ := filepath.Abs(dir)
		if absDir == absWd {
			allowed = true
			break
		}
	}
	if !allowed {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Directory is not a configured watch directory"})
	}

	search := strings.ToLower(c.QueryParam("search"))
	sortBy := c.QueryParam("sort")     // name, size, date (default: date)
	sortDir := c.QueryParam("sort_dir") // asc, desc (default: desc for date, asc for name/size)

	page := 1
	perPage := 25
	if v := c.QueryParam("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.QueryParam("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 100 {
				n = 100
			}
			perPage = n
		}
	}

	allFiles, err := listXMLFiles(dir)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to list directory: %v", err)})
	}

	// Filter by search term
	var filtered []WatchDirFile
	for _, f := range allFiles {
		if search == "" || strings.Contains(strings.ToLower(f.Name), search) {
			filtered = append(filtered, f)
		}
	}

	// Sort the filtered results
	asc := sortDir == "asc"
	switch sortBy {
	case "name":
		if !asc {
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name > filtered[j].Name })
		} else {
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
		}
	case "size":
		if asc {
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].Size < filtered[j].Size })
		} else {
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].Size > filtered[j].Size })
		}
	default: // "date" — default desc (newest first)
		if asc {
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].ModTime.Before(filtered[j].ModTime) })
		} else {
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].ModTime.After(filtered[j].ModTime) })
		}
	}

	total := len(filtered)
	totalPages := (total + perPage - 1) / perPage
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * perPage
	end := start + perPage
	if end > total {
		end = total
	}

	var pageFiles []WatchDirFile
	if start < total {
		pageFiles = filtered[start:end]
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"dir":         dir,
		"page":        page,
		"per_page":    perPage,
		"total":       total,
		"total_pages": totalPages,
		"sort":        sortBy,
		"sort_dir":    sortDir,
		"files":       pageFiles,
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
		if err := copyToIngest(req.Path, ingestDir); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("Failed to queue file: %v", err),
			})
		}

		filename := filepath.Base(req.Path)
		return c.JSON(http.StatusOK, map[string]string{
			"message":  fmt.Sprintf("File '%s' queued for import. It will be processed within 1 minute.", filename),
			"filename": filename,
		})
	}
}

// copyToIngest copies a single source file into ingestDir, skipping if already present
func copyToIngest(srcPath, ingestDir string) error {
	filename := filepath.Base(srcPath)
	destPath := filepath.Join(ingestDir, filename)

	// Skip files already queued
	if _, err := os.Stat(destPath); err == nil {
		return nil
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// HandleImportAllWatchDirs queues every XML file from all watch directories into the user's ingest dir
func HandleImportAllWatchDirs(dataDir string) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID, ok := c.Get("user_id").(string)
		if !ok || userID == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "User not authenticated",
			})
		}

		ingestDir := filepath.Join(dataDir, userID, "ingest")
		if err := os.MkdirAll(ingestDir, 0755); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("Failed to create ingest directory: %v", err),
			})
		}

		watchDirs := GetWatchDirs()
		queued := 0
		skipped := 0
		var errs []string

		for _, dir := range watchDirs {
			files, err := listXMLFiles(dir)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", dir, err))
				continue
			}
			for _, f := range files {
				destPath := filepath.Join(ingestDir, f.Name)
				if _, err := os.Stat(destPath); err == nil {
					skipped++
					continue
				}
				if err := copyToIngest(f.Path, ingestDir); err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", f.Name, err))
				} else {
					queued++
				}
			}
		}

		msg := fmt.Sprintf("Queued %d files for import", queued)
		if skipped > 0 {
			msg += fmt.Sprintf(" (%d already queued, skipped)", skipped)
		}
		if len(errs) > 0 {
			msg += fmt.Sprintf("; %d errors", len(errs))
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"message": msg,
			"queued":  queued,
			"skipped": skipped,
			"errors":  errs,
		})
	}
}
