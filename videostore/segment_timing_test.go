package videostore

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	vsutils "github.com/viam-modules/video-store/videostore/utils"
	"go.viam.com/rdk/logging"
	"go.viam.com/test"
)

const (
	rtpClockRate      = 90000
	testFPS           = 20
	testGOP           = 40
	testFrames        = 120
	testSegmentSecs   = 2
	h264NALUTypeAUD   = 9
	h264NALUTypeIDR   = 5
	frameDuration     = time.Second / testFPS
	ptsPerTestFrame   = rtpClockRate / testFPS
	segmentFrameCount = testSegmentSecs * testFPS
)

// encodeTestH264 encodes testFrames of 20 fps H264 with a fixed GOP, no B-frames and access unit
// delimiters, which is the shape of an IP camera stream after RTP depacketization.
func encodeTestH264(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg CLI not found")
	}
	out := filepath.Join(t.TempDir(), "src.h264")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi",
		"-i", "testsrc2=size=320x240:rate="+strconv.Itoa(testFPS),
		"-frames:v", strconv.Itoa(testFrames), "-c:v", "libx264", "-profile:v", "baseline", "-bf", "0",
		"-g", strconv.Itoa(testGOP), "-keyint_min", strconv.Itoa(testGOP), "-sc_threshold", "0",
		"-x264-params", "aud=1", "-f", "h264", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not encode the test stream (needs libx264): %v: %s", err, msg)
	}
	stream, err := os.ReadFile(out)
	test.That(t, err, test.ShouldBeNil)
	return stream
}

// splitAnnexBAUs splits an Annex B H264 stream into access units at its access unit delimiters,
// dropping the delimiters as the viamrtsp mux does.
func splitAnnexBAUs(stream []byte) (aus [][]byte, idr []bool) {
	var cur []byte
	curIDR := false
	flush := func() {
		if len(cur) > 0 {
			aus = append(aus, cur)
			idr = append(idr, curIDR)
		}
		cur, curIDR = nil, false
	}
	for _, nalu := range bytes.Split(stream, []byte{0, 0, 1}) {
		nalu = bytes.TrimRight(nalu, "\x00")
		if len(nalu) == 0 {
			continue
		}
		switch nalu[0] & 0x1F {
		case h264NALUTypeAUD:
			flush()
			continue
		case h264NALUTypeIDR:
			curIDR = true
		}
		cur = append(cur, 0, 0, 0, 1)
		cur = append(cur, nalu...)
	}
	flush()
	return aus, idr
}

func countPackets(t *testing.T, path string) int {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-count_packets", "-select_streams", "v:0",
		"-show_entries", "stream=nb_read_packets", "-of", "csv=p=0", path).Output()
	test.That(t, err, test.ShouldBeNil)
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	test.That(t, err, test.ShouldBeNil)
	return n
}

// TestSegmentConcatKeepsEveryFrame feeds the raw segmenter the way viamrtsp does (90 kHz RTP
// timestamps, packets without a duration) and checks that segments report their full length and
// that concatenating them keeps every frame. A segment whose last frame has no duration reports
// one frame short, and each concat join then drops a frame, so saved clips drift behind wall clock.
func TestSegmentConcatKeepsEveryFrame(t *testing.T) {
	aus, idr := splitAnnexBAUs(encodeTestH264(t))
	test.That(t, len(aus), test.ShouldEqual, testFrames)

	logger := logging.NewTestLogger(t)
	storage := t.TempDir()
	rs, err := newRawSegmenter(storage, logger)
	test.That(t, err, test.ShouldBeNil)
	rs.segmentSeconds = testSegmentSecs
	test.That(t, rs.Init(CodecTypeH264, 320, 240), test.ShouldBeNil)

	// Segment files are named after the wall-clock second they open in; writing faster than real
	// time but slower than one segment per second keeps the names distinct.
	pace := frameDuration * 3 / 5
	for i, au := range aus {
		pts := int64(i * ptsPerTestFrame)
		test.That(t, rs.WritePacket(au, pts, pts, idr[i]), test.ShouldBeNil)
		time.Sleep(pace)
	}
	test.That(t, rs.Close(), test.ShouldBeNil)

	files, err := vsutils.GetSortedFiles(storage)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, files, test.ShouldHaveLength, testFrames/segmentFrameCount)
	for _, f := range files {
		info, err := vsutils.GetVideoInfo(f.Name)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, countPackets(t, f.Name), test.ShouldEqual, segmentFrameCount)
		test.That(t, info.Duration, test.ShouldEqual, segmentFrameCount*frameDuration)
	}

	out := filepath.Join(t.TempDir(), "out.mp4")
	c, err := newConcater(storage, t.TempDir(), logger)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, c.Concat(files[0].StartTime, files[len(files)-1].StartTime.Add(time.Minute), out), test.ShouldBeNil)
	info, err := vsutils.GetVideoInfo(out)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, countPackets(t, out), test.ShouldEqual, testFrames)
	test.That(t, info.Duration, test.ShouldEqual, testFrames*frameDuration)
}
