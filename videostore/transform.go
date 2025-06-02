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
type transformProcessor struct {
	watchDir     string
	outputDir    string
	pendingFiles []string
	processLock  sync.Mutex
	logger       logging.Logger
}

// NewTransformProcessor creates a new processor
func NewTransformProcessor(watchDir, outputDir string, logger logging.Logger) *transformProcessor {
	return &transformProcessor{
		watchDir:     watchDir,
		outputDir:    outputDir,
		pendingFiles: []string{},
		logger:       logger,
	}
}

// ProcessSegments watches the directory and processes new files
func (tp *transformProcessor) ProcessSegments(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	if err := watcher.Add(tp.watchDir); err != nil {
		return fmt.Errorf("failed to add directory to watcher: %w", err)
	}
	tp.logger.Info("Starting to watch directory:", tp.watchDir)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher closed unexpectedly")
			}
			// Only process file creation events for MP4 files
			if event.Op&fsnotify.Create == fsnotify.Create && strings.HasSuffix(event.Name, ".mp4") {
				tp.logger.Info("MP4 file created:", event.Name)
				go tp.queueFile(event.Name)
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
func (tp *transformProcessor) queueFile(filePath string) {
	tp.processLock.Lock()
	defer tp.processLock.Unlock()

	tp.pendingFiles = append(tp.pendingFiles, filePath)
	tp.logger.Info("File queued:", filePath)

	// Process the oldest file when we have 2+ files (ensuring the previous is complete)
	if len(tp.pendingFiles) > 1 {
		fileToProcess := tp.pendingFiles[0]
		tp.pendingFiles = tp.pendingFiles[1:]

		// Give a moment for file operations to settle
		// time.Sleep(2 * time.Second)

		if err := tp.convertFileToUTC(fileToProcess); err != nil {
			tp.logger.Errorf("Failed to process %s: %v", fileToProcess, err)
		}
	}
}

// convertFileToUTC converts a file with local timestamp to UTC timestamp
func (tp *transformProcessor) convertFileToUTC(filePath string) error {
	filename := filepath.Base(filePath)
	// Extract date and time from filename (example: 2025-05-30_17-05-36.mp4)
	parts := strings.Split(strings.TrimSuffix(filename, ".mp4"), "_")
	if len(parts) != 2 {
		return fmt.Errorf("unexpected filename format: %s", filename)
	}
	dateStr := parts[0]
	timeStr := strings.ReplaceAll(parts[1], "-", ":")

	// Parse local time
	localTime, err := time.ParseInLocation("2006-01-02 15:04:05", dateStr+" "+timeStr, time.Local)
	if err != nil {
		return fmt.Errorf("failed to parse time from filename: %w", err)
	}

	// Convert to Unix timestamp
	unixTimestamp := localTime.Unix()
	unixFilename := fmt.Sprintf("%d.mp4", unixTimestamp)

	outputPath := filepath.Join(tp.outputDir, unixFilename)
	tp.logger.Infof("Converting %s to UTC: %s", filename, unixFilename)

	// rename the file to the new UTC filename
	if err := os.Rename(filePath, outputPath); err != nil {
		return fmt.Errorf("failed to rename file %s to %s: %w", filePath, outputPath, err)
	}

	return nil
}

// Close processes any remaining files in the queue and cleans up resources
func (tp *transformProcessor) Close() error {
	tp.logger.Info("Closing transformer, processing remaining files...")

	tp.processLock.Lock()
	defer tp.processLock.Unlock()

	// Process any remaining files in the queue
	var lastErr error
	for _, filePath := range tp.pendingFiles {
		tp.logger.Info("Processing remaining file:", filePath)

		if err := tp.convertFileToUTC(filePath); err != nil {
			tp.logger.Errorf("Failed to process %s during shutdown: %v", filePath, err)
			lastErr = err // Keep the last error to return
		}
	}

	// Clear the queue
	tp.pendingFiles = nil

	if lastErr != nil {
		return fmt.Errorf("errors occurred while processing remaining files: %w", lastErr)
	}

	return nil
}
