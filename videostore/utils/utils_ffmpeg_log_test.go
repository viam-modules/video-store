package vsutils

import (
	"bytes"
	"io"
	"os"
	"testing"

	"go.viam.com/test"
	"golang.org/x/sys/unix"
)

// captureFFmpegStreams redirects OS-level fd 1 (stdout) and fd 2 (stderr),
// runs fn(), then returns what was written to each stream.
// Go-level os.Pipe() is not sufficient because the C callback writes
// directly via fprintf(stdout/stderr), requiring fd-level redirection.
func captureFFmpegStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	origOut, err := unix.Dup(unix.Stdout)
	test.That(t, err, test.ShouldBeNil)
	origErr, err := unix.Dup(unix.Stderr)
	test.That(t, err, test.ShouldBeNil)

	rOut, wOut, err := os.Pipe()
	test.That(t, err, test.ShouldBeNil)
	rErr, wErr, err := os.Pipe()
	test.That(t, err, test.ShouldBeNil)

	err = unix.Dup2(int(wOut.Fd()), unix.Stdout)
	test.That(t, err, test.ShouldBeNil)
	err = unix.Dup2(int(wErr.Fd()), unix.Stderr)
	test.That(t, err, test.ShouldBeNil)

	fn()

	// Restore original fds first. Dup2 onto fd 1/2 closes their current reference
	// to the pipe write end; closing wOut/wErr drops the Go-side reference. Once
	// all write ends are closed io.Copy below will see EOF.
	err = unix.Dup2(origOut, unix.Stdout)
	test.That(t, err, test.ShouldBeNil)
	err = unix.Dup2(origErr, unix.Stderr)
	test.That(t, err, test.ShouldBeNil)
	unix.Close(origOut) //nolint:errcheck
	unix.Close(origErr) //nolint:errcheck
	wOut.Close()
	wErr.Close()

	// Read both streams concurrently to avoid blocking if one pipe buffer fills.
	var outBuf, errBuf bytes.Buffer
	done := make(chan struct{})
	go func() {
		io.Copy(&errBuf, rErr) //nolint:errcheck
		rErr.Close()
		close(done)
	}()
	io.Copy(&outBuf, rOut) //nolint:errcheck
	rOut.Close()
	<-done

	return outBuf.String(), errBuf.String()
}

func TestFFmpegLogRouting(t *testing.T) {
	SetFFmpegLogCallback()

	origLevel := getAVLogLevel()
	defer setAVLogLevel(origLevel)

	t.Run("warnings and above route to stderr", func(t *testing.T) {
		cases := []struct {
			level     int
			wantLabel string
		}{
			{avLogFatal, "[FFmpeg Fatal]"},
			{avLogError, "[FFmpeg Error]"},
			{avLogWarning, "[FFmpeg Warn]"},
		}
		setAVLogLevel(avLogDebug)
		for _, tc := range cases {
			t.Run(tc.wantLabel, func(t *testing.T) {
				stdout, stderr := captureFFmpegStreams(t, func() {
					emitAVLog(tc.level, "test routing message")
				})
				test.That(t, stderr, test.ShouldContainSubstring, tc.wantLabel)
				test.That(t, stderr, test.ShouldContainSubstring, "test routing message")
				test.That(t, stdout, test.ShouldBeEmpty)
			})
		}
	})

	t.Run("info and below route to stdout", func(t *testing.T) {
		cases := []struct {
			level     int
			wantLabel string
		}{
			{avLogInfo, "[FFmpeg Info]"},
			{avLogVerbose, "[FFmpeg Verbose]"},
			{avLogDebug, "[FFmpeg Debug]"},
		}
		setAVLogLevel(avLogDebug)
		for _, tc := range cases {
			t.Run(tc.wantLabel, func(t *testing.T) {
				stdout, stderr := captureFFmpegStreams(t, func() {
					emitAVLog(tc.level, "test routing message")
				})
				test.That(t, stdout, test.ShouldContainSubstring, tc.wantLabel)
				test.That(t, stdout, test.ShouldContainSubstring, "test routing message")
				test.That(t, stderr, test.ShouldBeEmpty)
			})
		}
	})

	t.Run("messages below log level are suppressed", func(t *testing.T) {
		setAVLogLevel(avLogWarning)
		stdout, stderr := captureFFmpegStreams(t, func() {
			emitAVLog(avLogInfo, "suppressed message")
		})
		test.That(t, stdout, test.ShouldBeEmpty)
		test.That(t, stderr, test.ShouldBeEmpty)
	})
}
