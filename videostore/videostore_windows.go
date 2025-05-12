//go:build windows || no_cgo
// +build windows no_cgo

package videostore

import (
	"context"
	"errors"
	"time"

	"go.viam.com/rdk/logging"
)

var errNotImplemented = errors.New("videostore functionality not implemented on Windows")

var presets = map[string]struct{}{
	"ultrafast": {},
	"superfast": {},
	"veryfast":  {},
	"faster":    {},
	"fast":      {},
	"medium":    {},
	"slow":      {},
	"slower":    {},
	"veryslow":  {},
}

// CodecType represents a codec.
type CodecType int

const (
	CodecTypeUnknown CodecType = iota
	CodecTypeH264
	CodecTypeH265
)

func (t CodecType) String() string {
	switch t {
	case CodecTypeUnknown:
		return "CodecTypeUnknown"
	case CodecTypeH264:
		return "CodecTypeH264"
	case CodecTypeH265:
		return "CodecTypeH265"
	default:
		return "CodecTypeUnknown"
	}
}

// -------- Stub Interfaces --------

type VideoStore interface {
	Fetch(ctx context.Context, r *FetchRequest) (*FetchResponse, error)
	Save(ctx context.Context, r *SaveRequest) (*SaveResponse, error)
	Close()
}

type RTPVideoStore interface {
	VideoStore
	Segmenter() *RawSegmenter
}

// -------- Stub Structs --------

type SaveRequest struct {
	From     time.Time
	To       time.Time
	Metadata string
	Async    bool
}

type SaveResponse struct {
	Filename string
}

type FetchRequest struct {
	From time.Time
	To   time.Time
}

type FetchResponse struct {
	Video []byte
}

// -------- Stub Constructors --------

func NewRTPVideoStore(any, logging.Logger) (RTPVideoStore, error) {
	return nil, errNotImplemented
}

func NewReadOnlyVideoStore(any, logging.Logger) (VideoStore, error) {
	return nil, errNotImplemented
}

func NewFramePollingVideoStore(any, logging.Logger) (VideoStore, error) {
	return nil, errNotImplemented
}

// -------- Stub Implementations --------

type RawSegmenter struct {
	logger logging.Logger
}

func (s *RawSegmenter) Close() error {
	return errNotImplemented
}

// Update method signature to match what mux.go expects
func (s *RawSegmenter) WritePacket([]byte, int64, int64, bool) error {
	return errNotImplemented
}

// Update method signature to match what mux.go expects
func (s *RawSegmenter) Init(CodecType, int, int) error {
	return errNotImplemented
}

func newRawSegmenter(storagePath string, logger logging.Logger) (*RawSegmenter, error) {
	return &RawSegmenter{logger: logger}, nil
}

type concater struct {
	logger logging.Logger
}

func newConcater(storagePath, uploadPath string, logger logging.Logger) (*concater, error) {
	return &concater{logger: logger}, nil
}

func (c *concater) Concat(from, to time.Time, outPath string) error {
	return errNotImplemented
}

type encoder struct {
	logger logging.Logger
}

func newEncoder(config any, framerate int, storagePath string, logger logging.Logger) (*encoder, error) {
	return &encoder{logger: logger}, nil
}

func (e *encoder) initialize() error {
	return errNotImplemented
}

func (e *encoder) encode(frame []byte) {}

func (e *encoder) close() {}

func createDir(path string) error {
	return nil
}

func generateOutputFilePath(prefix string, timestamp time.Time, metadata string, path string) string {
	return ""
}

func readVideoFile(path string) ([]byte, error) {
	return nil, errNotImplemented
}

// Add missing ParseDateTimeString function
func ParseDateTimeString(s string) (time.Time, error) {
	return time.Time{}, errNotImplemented
}
