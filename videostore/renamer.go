package videostore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"go.viam.com/rdk/logging"
)

// renamer watches a directory and converts local timestamps to unix timestamps
type renamer struct {
	watchDir     string
	outputDir    string
	pendingFiles []string
	processLock  sync.Mutex
	logger       logging.Logger
}

func newRenamer(watchDir, outputDir string, logger logging.Logger) *renamer {
	return &renamer{
		watchDir:     watchDir,
		outputDir:    outputDir,
		pendingFiles: []string{},
		logger:       logger,
	}
}

// processSegments watches the directory and processes new files
func (r *renamer) processSegments(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()
	if err := watcher.Add(r.watchDir); err != nil {
		return fmt.Errorf("failed to add directory to watcher: %w", err)
	}
	r.logger.Debugf("starting to watch directory:", r.watchDir)
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("watcher closed unexpectedly")
			}
			// Only process file creation events for MP4 files
			if event.Op&fsnotify.Create == fsnotify.Create && strings.HasSuffix(event.Name, ".mp4") {
				r.logger.Debug("mp4 file created:", event.Name)
				r.queueFile(event.Name)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return errors.New("watcher error channel closed unexpectedly")
			}
			return fmt.Errorf("watcher error: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// queueFile adds a file to the processing queue
// When we get a file creation event, the file is still being written to by the segmenter.
// The file is queued and only processed when the next segment file is created. We should never
// have more than 2 files in the queue at a time.
func (r *renamer) queueFile(filePath string) {
	r.processLock.Lock()
	defer r.processLock.Unlock()
	r.pendingFiles = append(r.pendingFiles, filePath)
	r.logger.Debug("files queued:", filePath)
	r.logger.Debug("queue files pending:", len(r.pendingFiles))
	if len(r.pendingFiles) > 1 {
		fileToProcess := r.pendingFiles[0]
		r.pendingFiles = r.pendingFiles[1:]
		if err := r.convertFileToUTC(fileToProcess); err != nil {
			r.logger.Errorf("failed to process %s: %v", fileToProcess, err)
		}
	}
}

// convertFileToUTC converts a file with local timestamp to unix timestamp
func (r *renamer) convertFileToUTC(filePath string) error {
	filename := filepath.Base(filePath)
	timestampStr := strings.TrimSuffix(filename, ".mp4")
	localTime, err := ParseDateTimeString(timestampStr)
	if err != nil {
		return fmt.Errorf("failed to parse timestamp from filename %s: %w", filename, err)
	}
	unixTimestamp := localTime.Unix()
	unixFilename := fmt.Sprintf("%d.mp4", unixTimestamp)
	outputPath := filepath.Join(r.outputDir, unixFilename)
	r.logger.Debugf("converting %s to UTC: %s", filename, unixFilename)
	if err := os.Rename(filePath, outputPath); err != nil {
		return fmt.Errorf("failed to rename file %s to %s: %w", filePath, outputPath, err)
	}

	return nil
}

// close processes any remaining files in the queue and cleans up resources
func (r *renamer) close() error {
	r.logger.Debug("closing renamer, processing remaining file")
	r.processLock.Lock()
	defer r.processLock.Unlock()
	// Process any remaining files in the queue
	var lastErr error
	for _, filePath := range r.pendingFiles {
		r.logger.Info("processing remaining file:", filePath)

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
