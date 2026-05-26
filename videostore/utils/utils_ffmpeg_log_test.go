package vsutils

import (
	"testing"

	"go.viam.com/rdk/logging"
	"go.viam.com/test"
)

func TestFFmpegLogRouting(t *testing.T) {
	origLevel := getAVLogLevel()
	defer setAVLogLevel(origLevel)

	t.Run("levels map to logger levels", func(t *testing.T) {
		cases := []struct {
			avLevel   int
			wantLevel logging.Level
		}{
			{avLogFatal, logging.ERROR},
			{avLogError, logging.ERROR},
			{avLogWarning, logging.WARN},
			{avLogInfo, logging.INFO},
			{avLogVerbose, logging.DEBUG},
			{avLogDebug, logging.DEBUG},
		}
		for _, tc := range cases {
			t.Run(tc.wantLevel.String(), func(t *testing.T) {
				logger, observed := logging.NewObservedTestLogger(t)
				SetFFmpegLogCallback(logger)
				setAVLogLevel(avLogDebug)

				emitAVLog(tc.avLevel, "test routing message")

				entries := observed.TakeAll()
				test.That(t, len(entries), test.ShouldEqual, 1)
				// observer reports zapcore.Level, convert from logging.Level.
				test.That(t, entries[0].Level, test.ShouldEqual, tc.wantLevel.AsZap())
				test.That(t, entries[0].Message, test.ShouldContainSubstring, "test routing message")
				test.That(t, entries[0].LoggerName, test.ShouldEndWith, "ffmpeg")
			})
		}
	})

	t.Run("messages below log level are suppressed", func(t *testing.T) {
		logger, observed := logging.NewObservedTestLogger(t)
		SetFFmpegLogCallback(logger)
		setAVLogLevel(avLogWarning)

		emitAVLog(avLogInfo, "suppressed message")

		test.That(t, len(observed.TakeAll()), test.ShouldEqual, 0)
	})
}
