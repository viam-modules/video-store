package videostore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

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

// processSegments periodically scans the directory for new files to process
//
// This function polls the directory regularly, looking for MP4 files
// and processing them in order of creation.
func (r *renamer) processSegments(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	r.logger.Debugf("Starting to scan directory: %s", r.watchDir)
	for {
		select {
		case <-ctx.Done():
			r.logger.Debug("Context done, stopping scanner and flushing remaining files")
			// if err := r.close(); err != nil {
			// 	return fmt.Errorf("error during close: %w", err)
			// }
			return nil
		case <-ticker.C:
			if err := r.scanAndProcessFiles(); err != nil {
				r.logger.Errorf("Error scanning directory: %v", err)
			}
		}
	}
}

// scanAndProcessFiles looks for files in the watch directory and processes them
func (r *renamer) scanAndProcessFiles() error {
	entries, err := os.ReadDir(r.watchDir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}
	// Filter for MP4 files
	var mpegFiles []string
	for _, entry := range entries {
		// Sanity skip for rogue dirs in our tmp path
		if entry.IsDir() {
			continue
		}
		// Find all MP4 files
		name := entry.Name()
		if strings.HasSuffix(name, ".mp4") {
			fullPath := filepath.Join(r.watchDir, name)
			mpegFiles = append(mpegFiles, fullPath)
		}
	}

	// Sort files by modification time (oldest first)
	// Since there should only ever be 2 files in the directory at a time,
	// we can afford to sort them when we scan the directory.
	sort.Slice(mpegFiles, func(i, j int) bool {
		infoI, _ := os.Stat(mpegFiles[i])
		infoJ, _ := os.Stat(mpegFiles[j])
		return infoI.ModTime().Before(infoJ.ModTime())
	})

	// Process files if we have any
	if len(mpegFiles) > 0 {
		r.processLock.Lock()
		defer r.processLock.Unlock()

		// Update our pending files list
		oldPending := r.pendingFiles
		r.pendingFiles = mpegFiles

		// Only process files we haven't seen before
		var newFiles []string
		for _, file := range mpegFiles {
			isNew := true
			for _, old := range oldPending {
				if file == old {
					isNew = false
					break
				}
			}
			if isNew {
				newFiles = append(newFiles, file)
			}
		}
		if len(newFiles) > 0 {
			r.logger.Debugf("Found %d new MP4 files", len(newFiles))
		}
		// Process all files except the most recent one
		if len(mpegFiles) > 1 {
			for _, fileToProcess := range mpegFiles[:len(mpegFiles)-1] {
				// Sanity check for rogue file deletion
				_, err := os.Stat(fileToProcess)
				if err != nil {
					if os.IsNotExist(err) {
						r.logger.Debugf("File %s does not exist, skipping", fileToProcess)
						continue
					}
					r.logger.Errorf("Failed to stat file %s: %v", fileToProcess, err)
					continue
				}
				// if fileInfo.Size() < 1024 {
				// 	r.logger.Debugf("Skipping small file %s (%d bytes)", fileToProcess, fileInfo.Size())
				// 	continue
				// }
				r.logger.Debugf("Processing file: %s", fileToProcess)
				if err := r.convertFilenameToUnixTimestamp(fileToProcess); err != nil {
					r.logger.Errorf("Failed to process %s: %v", fileToProcess, err)
				}
			}
		} else {
			r.logger.Debug("Only one active file found, skipping processing")
		}
	}

	return nil
}

// convertFilenameToUnixTimestamp converts a filename to unix timestamp format
//
// Converting to unix timestamp is only necessary on Windows systems where the
// local time is used in the filename. FFmpeg is unable to use strftime to format
// filenames in unix timestamp format, so we need to convert using golang here.
func (r *renamer) convertFilenameToUnixTimestamp(filePath string) error {
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

// close flushes any remaining files in the queue to disk
//
// When the renamer is closed, there will be a remaining file in the queue that
// still needs to be processed. The segmenter closes out before the renamer,
// so we can safely processes the last file during closeout.
func (r *renamer) close() error {
	r.logger.Debug("closing renamer, processing remaining file")
	r.processLock.Lock()
	defer r.processLock.Unlock()
	// Process any remaining files in the queue
	var lastErr error
	for _, filePath := range r.pendingFiles {
		r.logger.Info("processing remaining file:", filePath)

		if err := r.convertFilenameToUnixTimestamp(filePath); err != nil {
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
