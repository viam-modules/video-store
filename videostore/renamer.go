package videostore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.viam.com/rdk/logging"
)

// transformProcessor watches a directory and converts local timestamps to UTC
type renamer struct {
	watchDir     string
	outputDir    string
	pendingFiles []string
	processLock  sync.Mutex
	logger       logging.Logger
}

// NewTransformProcessor creates a new processor
func NewRenamer(watchDir, outputDir string, logger logging.Logger) *renamer {
	return &renamer{
		watchDir:     watchDir,
		outputDir:    outputDir,
		pendingFiles: []string{},
		logger:       logger,
	}
}

// ProcessSegments watches the directory and processes new files
func (r *renamer) ProcessSegments(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()
	if err := watcher.Add(r.watchDir); err != nil {
		return fmt.Errorf("failed to add directory to watcher: %w", err)
	}
	r.logger.Debugf("Renamer starting to watch directory:", r.watchDir)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher closed unexpectedly")
			}
			// Only process file creation events for MP4 files
			if event.Op&fsnotify.Create == fsnotify.Create && strings.HasSuffix(event.Name, ".mp4") {
				r.logger.Debug("MP4 file created:", event.Name)
				r.queueFile(event.Name)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("watcher error channel closed unexpectedly")
			}
			return fmt.Errorf("watcher error: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// queueFile adds a file to the processing queue
func (r *renamer) queueFile(filePath string) {
	r.processLock.Lock()
	defer r.processLock.Unlock()
	r.pendingFiles = append(r.pendingFiles, filePath)
	r.logger.Debug("Renamer file queued:", filePath)
	// Process the oldest file when we have 2+ files (ensuring the previous is complete)
	if len(r.pendingFiles) > 1 {
		fileToProcess := r.pendingFiles[0]
		r.pendingFiles = r.pendingFiles[1:]
		if err := r.convertFileToUTC(fileToProcess); err != nil {
			r.logger.Errorf("Failed to process %s: %v", fileToProcess, err)
		}
	}
}

// convertFileToUTC converts a file with local timestamp to unix timestamp
func (r *renamer) convertFileToUTC(filePath string) error {
	filename := filepath.Base(filePath)
	// Extract date and time from filename (example: 2025-05-30_17-05-36.mp4)
	parts := strings.Split(strings.TrimSuffix(filename, ".mp4"), "_")
	if len(parts) != 2 {
		return fmt.Errorf("unexpected filename format: %s", filename)
	}
	dateStr := parts[0]
	timeStr := strings.ReplaceAll(parts[1], "-", ":")
	localTime, err := time.ParseInLocation("2006-01-02 15:04:05", dateStr+" "+timeStr, time.Local)
	if err != nil {
		return fmt.Errorf("failed to parse time from filename: %w", err)
	}
	unixTimestamp := localTime.Unix()
	unixFilename := fmt.Sprintf("%d.mp4", unixTimestamp)
	outputPath := filepath.Join(r.outputDir, unixFilename)
	r.logger.Debugf("Converting %s to UTC: %s", filename, unixFilename)
	if err := os.Rename(filePath, outputPath); err != nil {
		return fmt.Errorf("failed to rename file %s to %s: %w", filePath, outputPath, err)
	}

	return nil
}

// Close processes any remaining files in the queue and cleans up resources
func (r *renamer) Close() error {
	r.logger.Debug("Closing renamer, processing remaining file")
	r.processLock.Lock()
	defer r.processLock.Unlock()
	// Process any remaining files in the queue
	var lastErr error
	for _, filePath := range r.pendingFiles {
		r.logger.Info("Processing remaining file:", filePath)

		if err := r.convertFileToUTC(filePath); err != nil {
			r.logger.Errorf("Failed to process %s during shutdown: %v", filePath, err)
			lastErr = err // Keep the last error to return
		}
	}
	r.pendingFiles = nil
	if lastErr != nil {
		return fmt.Errorf("errors occurred while processing remaining files: %w", lastErr)
	}

	return nil
}
