// Package indexer manages metadata for video files stored on disk.
package indexer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-sqlite3"
	vsutils "github.com/viam-modules/video-store/videostore/utils"
	"go.viam.com/rdk/logging"
	"go.viam.com/utils"
)

const (
	dbFileName                    = "index.sqlite.db"
	dbFileMode                    = 0o750
	filesTableName                = "files"
	videoFileSuffix               = ".mp4"
	refreshInterval               = 1 * time.Second
	slopDuration                  = 5 * time.Second
	unreadableFileGracePeriodMult = 5
	minUnreadableFileGracePeriod  = 30 * time.Minute
)

// diskFileEntry holds information about a file on disk being considered for indexing,
// including its extracted timestamp.
type diskFileEntry struct {
	info os.FileInfo
	time time.Time // extracted from the filename
}

// Indexer manages metadata for video files stored on disk.
type Indexer struct {
	logger                 logging.Logger
	storagePath            string
	storageMaxGB           int
	segmentDurationSeconds int
	dbPath                 string

	// dbMu protects the db pointer, not the db itself.
	dbMu sync.RWMutex
	db   *sql.DB

	workers *utils.StoppableWorkers
}

// fileMetadata holds metadata for an indexed video file.
type fileMetadata struct {
	FileName      string
	StartTimeUnix int64
	DurationMs    int64
	SizeBytes     int64
	Width         int
	Height        int
	Codec         string
}

// NewIndexer creates a new indexer instance.
func NewIndexer(storagePath string, storageMaxGB, segmentDurationSeconds int, logger logging.Logger) *Indexer {
	idx := &Indexer{
		logger:                 logger,
		storagePath:            storagePath,
		storageMaxGB:           storageMaxGB,
		segmentDurationSeconds: segmentDurationSeconds,
		dbPath:                 filepath.Join(storagePath, dbFileName),
		workers:                utils.NewBackgroundStoppableWorkers(),
	}
	return idx
}

// Start initializes the database and starts the indexer's background processing loop.
// It returns an error if the initial database setup fails.
func (ix *Indexer) Start(ctx context.Context) error {
	if err := ix.setupDB(ctx); err != nil {
		return fmt.Errorf("initial database setup failed: %w", err)
	}

	ix.workers.Add(ix.run)

	return nil
}

// setupDB initializes the underlying database and readies it for use.
// It is idempotent.
func (ix *Indexer) setupDB(ctx context.Context) error {
	ix.dbMu.Lock()
	defer ix.dbMu.Unlock()

	if ix.db != nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(ix.dbPath), dbFileMode); err != nil {
		return fmt.Errorf("failed to create directory for index db: %w", err)
	}
	db, err := sql.Open("sqlite3", ix.dbPath)
	if err != nil {
		return fmt.Errorf("failed to open index db: %w", err)
	}

	if initErr := initializeDB(ctx, db); initErr != nil {
		dbCloseErr := db.Close()
		if dbCloseErr != nil {
			ix.logger.Errorw(
				"failed to close db after initialization error",
				"db close err", dbCloseErr,
				"initialization err", initErr,
			)
		}
		return fmt.Errorf("failed to initialize index db schema: %w", initErr)
	}

	ix.db = db // intentionally assign only after successful initialization.
	return nil
}

// initializeDB initializes the database schema.
func initializeDB(ctx context.Context, db *sql.DB) error {
	query := fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS %s (
		id INTEGER NOT NULL PRIMARY KEY,
		file_name TEXT NOT NULL UNIQUE,
		start_time_unix INTEGER NOT NULL,
		duration_ms INTEGER NOT NULL,
		size_bytes INTEGER NOT NULL,
		width INTEGER NOT NULL,
		height INTEGER NOT NULL,
		codec TEXT NOT NULL,
		inserted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
		deleted_at TIMESTAMP
	);
	`, filesTableName)

	_, err := db.ExecContext(ctx, query)
	return err
}

// run starts the indexer event loop.
func (ix *Indexer) run(ctx context.Context) {
	ix.logger.Debug("running indexer")
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			ix.logger.Debug("indexer has stopped")
			return
		case <-ticker.C:
			// This is not the initial setting up of the db, but rather to ensure recovery from manual db deletions
			err := ix.setupDB(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					ix.logger.Debugw("context done, stopping refresh cycle(1)", "reason", err)
					return
				}
				ix.logger.Errorw("error setting up indexer", "error", err)
				continue
			}
			err = ix.refreshIndexAndStorage(ctx)
			if err != nil {
				if isDBReadOnlyError(err) {
					ix.logger.Warnw("readonly DB error detected by indexer refresh, attempting re-setup.", "error", err)
					ix.dbMu.Lock()
					if ix.db != nil {
						dbCloseErr := ix.db.Close()
						if dbCloseErr != nil {
							ix.logger.Errorw("error closing readonly db connection", "error", dbCloseErr)
						}
						ix.db = nil
						ix.logger.Debug("readonly database connection closed, will recreate on next tick")
					}
					ix.dbMu.Unlock()
				} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					ix.logger.Debugw("context done, stopping refresh cycle(2)", "reason", err)
					return
				} else {
					ix.logger.Errorw("error refreshing indexer", "error", err)
				}
			}
		}
	}
}

func isDBReadOnlyError(err error) bool {
	var sErr sqlite3.Error
	return errors.As(err, &sErr) && sErr.Code == sqlite3.ErrReadonly
}

// refreshIndexAndStorage refreshes the index and storage.
func (ix *Indexer) refreshIndexAndStorage(ctx context.Context) error {
	ix.dbMu.RLock()
	defer ix.dbMu.RUnlock()
	if ix.db == nil {
		return errors.New("indexer not setup")
	}

	start := time.Now()

	// 1. Process unindexed files
	if err := ix.processUnindexedFiles(ctx); err != nil {
		return err
	}
	ix.logger.Debugf("TIMING: processUnindexedFiles took %v", time.Since(start))

	// 2. Mark files to delete based on storage limits
	cleanupDBStart := time.Now()
	if err := ix.cleanupDB(ctx); err != nil {
		return err
	}
	ix.logger.Debugf("TIMING: cleanupDB took %v", time.Since(cleanupDBStart))

	// 3. Delete marked files from disk and their records from DB
	cleanupFilesStart := time.Now()
	if err := ix.cleanupFiles(ctx); err != nil {
		return err
	}
	ix.logger.Debugf("TIMING: cleanupFiles took %v", time.Since(cleanupFilesStart))

	ix.logger.Debugf("TIMING: refreshIndexAndStorage completed in %v", time.Since(start))
	return nil
}

// processUnindexedFiles indexes video files on disk. Assumes the dbMu is read-locked.
// It adds all new valid video files, and deletes corrupt/invalid video files from the storage dir.
func (ix *Indexer) processUnindexedFiles(ctx context.Context) error {
	indexedFiles, err := ix.getIndexedFiles(ctx)
	if err != nil {
		return fmt.Errorf("failed to get indexed files: %w", err)
	}

	diskFileEntries, err := ix.getDiskFilesSorted(ctx)
	if err != nil {
		return fmt.Errorf("failed to get and sort disk files: %w", err)
	}

	for _, entry := range diskFileEntries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		fileName := entry.info.Name()
		if _, exists := indexedFiles[fileName]; exists {
			continue
		}

		err := ix.processFileForIndex(ctx, fileName, entry.info.Size())
		if err != nil {
			if isDBReadOnlyError(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			ix.logger.Errorw("failed to index new file", "name", fileName, "error", err)
		}
	}

	if err := ix.handleMissingDiskFiles(ctx, indexedFiles); err != nil {
		return fmt.Errorf("failed to handle missing disk files: %w", err)
	}

	return nil
}

// getIndexedFiles returns a set of all non-deleted indexed video file names from the db.
// Assumes the dbMu is read-locked.
func (ix *Indexer) getIndexedFiles(ctx context.Context) (map[string]struct{}, error) {
	query := "SELECT file_name FROM " + filesTableName + " WHERE deleted_at IS NULL;"
	rows, err := ix.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := make(map[string]struct{})
	for rows.Next() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var fileName string
		if err := rows.Scan(&fileName); err != nil {
			return nil, err
		}
		files[fileName] = struct{}{}
	}
	return files, rows.Err()
}

// getDiskFilesSorted returns a slice of diskFileEntry for all valid video files on disk,
// sorted by their extracted timestamp (newest first).
// Files with unparseable names are logged and skipped.
func (ix *Indexer) getDiskFilesSorted(ctx context.Context) ([]diskFileEntry, error) {
	entries, err := os.ReadDir(ix.storagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read storage directory %s: %w", ix.storagePath, err)
	}

	sortableFiles := []diskFileEntry{}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if entry.IsDir() || !strings.HasSuffix(entry.Name(), videoFileSuffix) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			ix.logger.Warnw("failed to get FileInfo for disk entry, skipping", "entry_name", entry.Name(), "error", err)
			continue
		}

		extractedTime, err := vsutils.ExtractDateTimeFromFilename(info.Name())
		if err != nil {
			ix.logger.Warnw("failed to extract timestamp from filename, skipping file", "name", info.Name(), "error", err)
			continue
		}

		sortableFiles = append(sortableFiles, diskFileEntry{info: info, time: extractedTime})
	}

	// Sort files by extracted time (newest first)
	sort.Slice(sortableFiles, func(i, j int) bool {
		return sortableFiles[i].time.After(sortableFiles[j].time)
	})
	return sortableFiles, nil
}

// processFileForIndex is a helper function that processes an untracked video file in the db. Assumes the dbMu is read-locked.
// It adds new valid video files, and deletes corrupt/invalid video files from the storage dir.
func (ix *Indexer) processFileForIndex(ctx context.Context, fileName string, fileSize int64) error {
	startTime, err := vsutils.ExtractDateTimeFromFilename(fileName)
	if err != nil {
		ix.logger.Warnw("failed to extract timestamp from filename, skipping", "file", fileName, "error", err)
		return nil
	}

	fullFilePath := filepath.Join(ix.storagePath, fileName)
	info, err := vsutils.GetVideoInfo(fullFilePath)
	if err != nil {
		// File is unreadable. Check if it's old enough to safely delete.
		// Recent files might still be written by the segmenter, so we use a grace period
		// based on segment duration to avoid deleting files that are actively being written.
		fileAge := time.Since(startTime)
		gracePeriod := time.Duration(ix.segmentDurationSeconds*unreadableFileGracePeriodMult) * time.Second
		if gracePeriod < minUnreadableFileGracePeriod {
			gracePeriod = minUnreadableFileGracePeriod
		}

		if fileAge < gracePeriod {
			ix.logger.Debugw("unreadable file is recent, skipping for now",
				"file", fileName,
				"age", fileAge,
				"recency_grace_period", gracePeriod,
				"error", err,
			)
			return nil
		}

		ix.logger.Warnw("deleting old unreadable file",
			"file", fileName,
			"age", fileAge,
			"size_bytes", fileSize,
			"error", err,
		)

		if delErr := os.Remove(fullFilePath); delErr != nil {
			ix.logger.Errorw("failed to delete unreadable file",
				"file", fileName,
				"error", delErr,
			)
		} else {
			ix.logger.Infow("deleted old unreadable file",
				"file", fileName,
				"age", fileAge,
			)
		}

		return nil
	}
	durationMs := info.Duration.Milliseconds()

	query := fmt.Sprintf(
		"INSERT INTO %s (file_name, start_time_unix, duration_ms, size_bytes, width, height, codec) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?) "+
			"ON CONFLICT(file_name) DO NOTHING;",
		filesTableName,
	)
	_, err = ix.db.ExecContext(
		ctx,
		query,
		fileName,
		startTime.Unix(),
		durationMs,
		fileSize,
		info.Width,
		info.Height,
		info.Codec,
	)
	if err != nil {
		return fmt.Errorf("failed to insert file into index: %w", err)
	}

	startTimeStr := vsutils.FormatUTC(startTime)
	ix.logger.Debugw("indexed new file", "file", fileName, "start_time", startTimeStr, "duration_ms", durationMs, "size_bytes", fileSize)
	return nil
}

// handleMissingDiskFiles checks for files that are indexed in the DB but no longer exist on disk.
// It hard-deletes the records for such files from the database.
func (ix *Indexer) handleMissingDiskFiles(ctx context.Context, indexedFiles map[string]struct{}) error {
	for indexedFileName := range indexedFiles {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fullPath := filepath.Join(ix.storagePath, indexedFileName)
		if _, statErr := os.Stat(fullPath); os.IsNotExist(statErr) {
			ix.logger.Debugw("indexed file not found on disk, hard-deleting record", "file", indexedFileName)
			if err := ix.deleteFileRecord(ctx, indexedFileName); err != nil {
				return fmt.Errorf("failed to hard-delete record for missing disk file %s: %w", indexedFileName, err)
			}
		}
	}
	return nil
}

// cleanupDB determines which files should be deleted based on storage limits
// and marks them in the database by setting deleted_at. Assumes the dbMu is read-locked.
func (ix *Indexer) cleanupDB(ctx context.Context) error {
	maxStorageSizeBytes := int64(ix.storageMaxGB) * vsutils.Gigabyte

	currentSizeBytes, err := vsutils.GetDirectorySize(ix.storagePath)
	if err != nil {
		return fmt.Errorf("failed to get directory size: %w", err)
	}

	if currentSizeBytes < maxStorageSizeBytes {
		return nil
	}

	bytesToDelete := currentSizeBytes - maxStorageSizeBytes
	ix.logger.Debugw("storage over limit, marking files for deletion",
		"current_bytes", currentSizeBytes,
		"max_bytes", maxStorageSizeBytes,
		"bytes_to_delete", bytesToDelete,
	)

	files, err := ix.getFilesAscTime(ctx)
	if err != nil {
		return fmt.Errorf("failed to get files for cleanup: %w", err)
	}

	var filesToMarkDeleted []string
	var bytesMarkedForDeletion int64
	for _, file := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		filesToMarkDeleted = append(filesToMarkDeleted, file.FileName)
		bytesMarkedForDeletion += file.SizeBytes
		if bytesMarkedForDeletion >= bytesToDelete {
			break
		}
	}

	if len(filesToMarkDeleted) == 0 {
		ix.logger.Warnw("storage over limit but no indexed files to delete",
			"current_bytes", currentSizeBytes,
			"max_bytes", maxStorageSizeBytes,
			"indexed_file_count", len(files),
		)
		return nil
	}

	err = ix.markFilesAsDeleted(ctx, filesToMarkDeleted)
	if err != nil {
		return fmt.Errorf("failed to mark files as deleted in DB: %w", err)
	}

	ix.logger.Debugw("marked files for deletion",
		"file_count", len(filesToMarkDeleted),
		"bytes_to_free", bytesMarkedForDeletion,
	)

	return nil
}

// markFilesAsDeleted sets the deleted_at timestamp for the given file names. Assumes the dbMu is read-locked.
func (ix *Indexer) markFilesAsDeleted(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}

	query := fmt.Sprintf(
		"UPDATE %s SET deleted_at = ? WHERE file_name = ? AND deleted_at IS NULL;",
		filesTableName,
	)

	currentTime := time.Now().UTC()
	for _, name := range names {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err := ix.db.ExecContext(ctx, query, currentTime, name)
		if err != nil {
			return fmt.Errorf("failed to mark file '%s' as deleted: %w", name, err)
		}
	}
	return nil
}

// cleanupFiles queries for files marked with deleted_at,
// deletes their files from disk, and then removes their records from the database.
// Assumes the dbMu is read-locked.
func (ix *Indexer) cleanupFiles(ctx context.Context) error {
	ix.logger.Debug("starting cleanupFiles: querying for files marked as deleted")

	query := fmt.Sprintf("SELECT file_name FROM %s WHERE deleted_at IS NOT NULL;", filesTableName)
	rows, err := ix.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query for soft-deleted files: %w", err)
	}
	defer rows.Close()

	// Scan db and accumulate files to be deleted (marked for deletion as per deleted_at)
	var filesToDelete []string
	for rows.Next() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var fileName string
		if err := rows.Scan(&fileName); err != nil {
			return fmt.Errorf("failed to scan soft-deleted path: %w", err)
		}
		filesToDelete = append(filesToDelete, fileName)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating over soft-deleted paths: %w", err)
	}

	if len(filesToDelete) == 0 {
		ix.logger.Debug("no soft-deleted files found to process in cleanupFiles")
		return nil
	}

	for _, name := range filesToDelete {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Delete from disk
		fullFilePath := filepath.Join(ix.storagePath, name)
		fileErr := os.Remove(fullFilePath)
		if fileErr == nil {
			ix.logger.Debugf("deleted file from disk: %s", fullFilePath)
		} else if os.IsNotExist(fileErr) {
			ix.logger.Warnf("file already deleted from disk or never existed: %s", fullFilePath)
		} else {
			return fmt.Errorf("failed to delete file from disk: %w", fileErr)
		}

		// Hard delete from database
		if err := ix.deleteFileRecord(ctx, name); err != nil {
			return err
		}
	}

	return nil
}

// deleteFileRecord hard-deletes a record from the database for the given file name.
func (ix *Indexer) deleteFileRecord(ctx context.Context, name string) error {
	deleteQuery := fmt.Sprintf("DELETE FROM %s WHERE file_name = ?;", filesTableName)
	_, dbErr := ix.db.ExecContext(ctx, deleteQuery, name)
	if dbErr != nil {
		return fmt.Errorf("failed to hard-delete record from database: %w", dbErr)
	}
	return nil
}

// VideoRange represents a single contiguous block of stored files.
type VideoRange struct {
	Start time.Time
	End   time.Time
}

// VideoRanges summarizes the state of the stored files.
type VideoRanges struct {
	StorageUsedBytes int64
	TotalDurationMs  int64
	VideoCount       int
	Ranges           []VideoRange
}

// GetVideoList returns the full list of video range structs from the db.
func (ix *Indexer) GetVideoList(ctx context.Context) (VideoRanges, error) {
	ix.dbMu.RLock()
	defer ix.dbMu.RUnlock()
	if ix.db == nil {
		return VideoRanges{}, errors.New("indexer not setup")
	}

	start := time.Now()
	files, err := ix.getFilesAscTime(ctx)
	if err != nil {
		return VideoRanges{}, fmt.Errorf("failed to fetch files for state: %w", err)
	}
	ix.logger.Debugf("TIMING: getting files asc time took %v", time.Since(start))

	return getVideoRangesFromFiles(files), nil
}

// getFilesAscTime is a helper function that retrieves all non-deleted files from the database,
// ordered by start time. Assumes the dbMu is read-locked.
func (ix *Indexer) getFilesAscTime(ctx context.Context) ([]fileMetadata, error) {
	query := fmt.Sprintf(`
	SELECT file_name, start_time_unix, duration_ms, size_bytes, width, height, codec
	FROM %s
	WHERE deleted_at IS NULL
	ORDER BY start_time_unix ASC;
	`, filesTableName)

	rows, err := ix.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query all files: %w", err)
	}
	defer rows.Close()

	var files []fileMetadata

	for rows.Next() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var fm fileMetadata

		if err := rows.Scan(&fm.FileName, &fm.StartTimeUnix, &fm.DurationMs, &fm.SizeBytes, &fm.Width, &fm.Height, &fm.Codec); err != nil {
			return nil, fmt.Errorf("failed to scan file row during full query: %w", err)
		}

		files = append(files, fm)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over all files query result: %w", err)
	}

	ix.logger.Debugf("retrieved %d files from index", len(files))
	return files, nil
}

// getVideoRangesFromFiles processes a slice of file metadata to produce videoRanges.
func getVideoRangesFromFiles(files []fileMetadata) VideoRanges {
	var vr VideoRanges

	var prevRange *VideoRange
	for _, f := range files {
		vr.VideoCount++
		vr.TotalDurationMs += f.DurationMs
		vr.StorageUsedBytes += f.SizeBytes

		fileStart := time.Unix(f.StartTimeUnix, 0)
		fileEnd := fileStart.Add(time.Duration(f.DurationMs) * time.Millisecond)

		if prevRange == nil {
			prevRange = &VideoRange{Start: fileStart, End: fileEnd}
		} else {
			if fileStart.After(prevRange.End.Add(slopDuration)) {
				// make a new range as there is too big of a gap between the prev file and the new file
				vr.Ranges = append(vr.Ranges, *prevRange)
				prevRange = &VideoRange{Start: fileStart, End: fileEnd}
			} else {
				// extend range
				prevRange.End = fileEnd
			}
		}
	}
	if prevRange != nil {
		vr.Ranges = append(vr.Ranges, *prevRange)
	}
	return vr
}

// Close stops the indexer's background processing and closes the underlying database.
func (ix *Indexer) Close() error {
	ix.workers.Stop()

	ix.dbMu.Lock()
	defer ix.dbMu.Unlock()
	if ix.db != nil {
		err := ix.db.Close()
		if err != nil {
			return fmt.Errorf("error closing index db: %w", err)
		}
		ix.db = nil
		ix.logger.Debug("indexer closed")
	}
	return nil
}
