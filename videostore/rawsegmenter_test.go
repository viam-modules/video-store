package videostore

import (
	"os"
	"testing"

	"go.viam.com/rdk/logging"
	"go.viam.com/test"
)

// TestRawSegmenterCloseAfterWriteFailure covers a segmenter whose writes fail, as they do on a
// full disk. Close must free it even though writing the trailer fails, so a second Close is a
// no-op and Init works again. Before RSDK-14934 the first Close returned an error but kept the
// FFmpeg context that av_write_trailer had already torn down, and the next Close segfaulted.
func TestRawSegmenterCloseAfterWriteFailure(t *testing.T) {
	aus, idr := splitAnnexBAUs(encodeTestH264(t))
	logger := logging.NewTestLogger(t)
	storage := t.TempDir()
	rs, err := newRawSegmenter(storage, logger)
	test.That(t, err, test.ShouldBeNil)
	rs.segmentSeconds = testSegmentSecs
	test.That(t, rs.Init(CodecTypeH264, 320, 240), test.ShouldBeNil)

	// With the storage directory gone, opening the next segment fails, which leaves the segment
	// muxer in the same state as running out of disk space.
	test.That(t, os.RemoveAll(storage), test.ShouldBeNil)
	var writeErr error
	for i, au := range aus {
		pts := int64(i * ptsPerTestFrame)
		if err := rs.WritePacket(au, pts, pts, idr[i]); err != nil {
			writeErr = err
		}
	}
	test.That(t, writeErr, test.ShouldNotBeNil)

	test.That(t, rs.Close(), test.ShouldNotBeNil)
	test.That(t, rs.Close(), test.ShouldBeNil)
	test.That(t, rs.WritePacket(aus[0], 0, 0, idr[0]), test.ShouldNotBeNil)

	// Once the directory is back, the same segmenter records again.
	test.That(t, os.MkdirAll(storage, 0o755), test.ShouldBeNil)
	test.That(t, rs.Init(CodecTypeH264, 320, 240), test.ShouldBeNil)
	for i, au := range aus[:segmentFrameCount] {
		pts := int64(i * ptsPerTestFrame)
		test.That(t, rs.WritePacket(au, pts, pts, idr[i]), test.ShouldBeNil)
	}
	test.That(t, rs.Close(), test.ShouldBeNil)
}
