package indexer

import (
	"testing"
	"time"

	// SQLite driver.
	_ "github.com/mattn/go-sqlite3"
	"go.viam.com/test"
)

func TestGetVideoRangesFromFiles(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	testCases := []struct {
		name           string
		files          []fileMetadata
		expectedRanges VideoRanges
	}{
		{
			name:           "empty files",
			files:          []fileMetadata{},
			expectedRanges: VideoRanges{},
		},
		{
			name: "zero duration file",
			files: []fileMetadata{
				{FileName: "vid1.mp4", StartTimeUnix: baseTime.Unix(), DurationMs: 0, SizeBytes: 100},
			},
			expectedRanges: VideoRanges{
				StorageUsedBytes: 100,
				TotalDurationMs:  0,
				VideoCount:       1,
				Ranges: []VideoRange{
					{Start: baseTime, End: baseTime},
				},
			},
		},
		{
			name: "multiple slop duration gaps",
			files: []fileMetadata{
				{FileName: "vid1.mp4", StartTimeUnix: baseTime.Unix(), DurationMs: 10000, SizeBytes: 100},
				{FileName: "vid2.mp4", StartTimeUnix: baseTime.Add(20 * time.Second).Unix(), DurationMs: 10000, SizeBytes: 150},
				{FileName: "vid3.mp4", StartTimeUnix: baseTime.Add(40 * time.Second).Unix(), DurationMs: 10000, SizeBytes: 200},
			},
			expectedRanges: VideoRanges{
				StorageUsedBytes: 450,
				TotalDurationMs:  30000,
				VideoCount:       3,
				Ranges: []VideoRange{
					{Start: baseTime, End: baseTime.Add(10 * time.Second)},
					{Start: baseTime.Add(20 * time.Second), End: baseTime.Add(30 * time.Second)},
					{Start: baseTime.Add(40 * time.Second), End: baseTime.Add(50 * time.Second)},
				},
			},
		},
		{
			name: "merge contiguous, gap splits",
			files: []fileMetadata{
				// vid1: 0s-10s
				{FileName: "vid1.mp4", StartTimeUnix: baseTime.Unix(), DurationMs: 10000, SizeBytes: 100},
				// vid2: 10s-20s (contiguous with vid1)
				{FileName: "vid2.mp4", StartTimeUnix: baseTime.Add(10 * time.Second).Unix(), DurationMs: 10000, SizeBytes: 150},
				// vid3: 30s-40s (gap > slopDuration from vid2)
				{FileName: "vid3.mp4", StartTimeUnix: baseTime.Add(30 * time.Second).Unix(), DurationMs: 10000, SizeBytes: 200},
			},
			expectedRanges: VideoRanges{
				StorageUsedBytes: 450,
				TotalDurationMs:  30000,
				VideoCount:       3,
				Ranges: []VideoRange{
					{Start: baseTime, End: baseTime.Add(20 * time.Second)},
					{Start: baseTime.Add(30 * time.Second), End: baseTime.Add(40 * time.Second)},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resultRanges := getVideoRangesFromFiles(tc.files)

			test.That(t, resultRanges.StorageUsedBytes, test.ShouldEqual, tc.expectedRanges.StorageUsedBytes)
			test.That(t, resultRanges.TotalDurationMs, test.ShouldEqual, tc.expectedRanges.TotalDurationMs)
			test.That(t, resultRanges.VideoCount, test.ShouldEqual, tc.expectedRanges.VideoCount)

			test.That(t, len(resultRanges.Ranges), test.ShouldEqual, len(tc.expectedRanges.Ranges))

			for i := range tc.expectedRanges.Ranges {
				expectedR := tc.expectedRanges.Ranges[i]
				actualR := resultRanges.Ranges[i]

				test.That(t, actualR.Start, test.ShouldEqual, expectedR.Start)
				test.That(t, actualR.End, test.ShouldEqual, expectedR.End)
			}
		})
	}
}
