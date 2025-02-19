package videostore

/*
#include <libavfilter/avfilter.h>
#include <libavutil/avutil.h>
#include <libavformat/avformat.h>
#include <libavutil/dict.h>
#include <libavutil/error.h>
#include <libavcodec/avcodec.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"unsafe"

	"github.com/bluenviron/mediacommon/pkg/codecs/h264"
	"go.viam.com/rdk/logging"
)

type rawSegmenter struct {
	logger         logging.Logger
	storagePath    string
	segmentSeconds int
	outCtxMu       sync.Mutex
	outCtx         *C.AVFormatContext
}

func newRawSegmenter(
	logger logging.Logger,
	storagePath string,
	segmentSeconds int,
) (*rawSegmenter, error) {
	s := &rawSegmenter{
		logger:         logger,
		storagePath:    storagePath,
		segmentSeconds: segmentSeconds,
	}
	err := createDir(s.storagePath)
	if err != nil {
		return nil, err
	}

	return s, nil
}

func (rs *rawSegmenter) initH264(sps, pps []byte) error {
	if len(sps) == 0 || len(pps) == 0 {
		return errors.New("both sps & pps must not be empty")
	}

	var hsps h264.SPS
	if err := hsps.Unmarshal(sps); err != nil {
		return err
	}
	rs.outCtxMu.Lock()
	defer rs.outCtxMu.Unlock()
	if rs.outCtx != nil {
		return errors.New("*rawSegmenter init called more than once")
	}
	// Allocate output context for segmenter. The "segment" format is a special format
	// that allows for segmenting output files. The output pattern is a strftime pattern
	// that specifies the output file name. The pattern is set to the current time.
	outputPatternCStr := C.CString(rs.storagePath + "/" + outputPattern)
	defer C.free(unsafe.Pointer(outputPatternCStr))
	formatName := C.CString("segment")
	defer C.free(unsafe.Pointer(formatName))
	var fmtCtx *C.AVFormatContext
	ret := C.avformat_alloc_output_context2(&fmtCtx, nil, formatName, outputPatternCStr)
	if ret < 0 {
		return fmt.Errorf("failed to allocate output context: %s", ffmpegError(ret))
	}
	rs.outCtx = fmtCtx

	// Create new stream for the output context.
	stream := C.avformat_new_stream(fmtCtx, nil)
	if stream == nil {
		return errors.New("failed to allocate stream")
	}
	stream.id = C.int(fmtCtx.nb_streams) - 1

	// Set the codec parameters for the output stream.
	codec := C.avcodec_find_encoder(C.AV_CODEC_ID_H264)
	if codec == nil {
		return errors.New("failed to find codec")
	}

	codecCtx := C.avcodec_alloc_context3(codec)
	if codecCtx == nil {
		return errors.New("failed to allocate codec context")
	}

	codecCtx.width = C.int(hsps.Width())
	codecCtx.height = C.int(hsps.Height())

	// Set up the extradata for the codec context. This allows the muxer to know how to
	// decode the stream without having to read the SPS/PPS from the stream.
	extradata, err := buildAVCExtradata(sps, pps)
	if err != nil {
		rs.logger.Error("failed to build extradata: ", err)
		return err
	}
	extradataSize := len(extradata)
	// Allocate extradata in the codec context with required padding.
	codecCtx.extradata = (*C.uint8_t)(C.av_malloc(C.size_t(extradataSize + C.AV_INPUT_BUFFER_PADDING_SIZE)))
	if codecCtx.extradata == nil {
		return errors.New("failed to allocate extradata")
	}
	C.memcpy(unsafe.Pointer(codecCtx.extradata), unsafe.Pointer(&extradata[0]), C.size_t(extradataSize))
	codecCtx.extradata_size = C.int(extradataSize)

	// Copy the codec parameters from the input stream to the output stream. Thi is equivalent
	// to -c:v copy in ffmpeg cli. This is needed to make sure we do not re-encode the stream.
	if ret := C.avcodec_parameters_from_context(stream.codecpar, codecCtx); ret < 0 {
		return fmt.Errorf("failed to copy codec parameters: %s", ffmpegError(ret))
	}

	// Set up segmenting parameters.
	// TODO(seanp): Make these configurable.
	segmentLengthCStr := C.CString(strconv.Itoa(rs.segmentSeconds))
	segmentFormatCStr := C.CString("mp4")
	resetTimestampsCStr := C.CString("1")
	strftimeCStr := C.CString("1")
	defer func() {
		C.free(unsafe.Pointer(segmentLengthCStr))
		C.free(unsafe.Pointer(segmentFormatCStr))
		C.free(unsafe.Pointer(resetTimestampsCStr))
		C.free(unsafe.Pointer(strftimeCStr))
	}()

	// Segment options are passed as opts to avformat_write_header. This only needs
	// to be done once, and not for every segment file.
	var opts *C.AVDictionary
	defer C.av_dict_free(&opts)
	ret = C.av_dict_set(&opts, C.CString("segment_time"), segmentLengthCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set segment_time: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("segment_format"), segmentFormatCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set segment_format: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("reset_timestamps"), resetTimestampsCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set reset_timestamps: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("strftime"), strftimeCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set strftime: %s", ffmpegError(ret))
	}

	// Open the output file for writing
	ret = C.avformat_write_header(fmtCtx, &opts)
	if ret < 0 {
		return fmt.Errorf("failed to write header: %s", ffmpegError(ret))
	}

	return nil
}

func (rs *rawSegmenter) writePacket(payload []byte, pts int64, isIDR bool) error {
	rs.outCtxMu.Lock()
	defer rs.outCtxMu.Unlock()
	if rs.outCtx == nil {
		return errors.New("writePacket called before initH264")
	}
	// Stuff the bytes payload and timestamps into an AV Packet.
	avpkt := C.av_packet_alloc()
	if avpkt == nil {
		return errors.New("failed to allocate AVPacket")
	}
	defer C.av_packet_free(&avpkt)
	// TODO(seanp): should i use (*C.uint8_t)(unsafe.Pointer(&payload[0])) or C.CBytes(payload) instead?
	avpkt.data = (*C.uint8_t)(C.av_malloc(C.size_t(len(payload))))
	if avpkt.data == nil {
		C.av_packet_free(&avpkt)
		return errors.New("failed to allocate AVPacket data")
	}
	// Copy the contents of the Go byte slice into the allocated memory
	C.memcpy(unsafe.Pointer(avpkt.data), unsafe.Pointer(&payload[0]), C.size_t(len(payload)))
	avpkt.size = C.int(len(payload))
	if avpkt.size == 0 {
		return errors.New("empty packet")
	}
	if avpkt.data == nil {
		return errors.New("nil packet data")
	}
	avpkt.pts = C.int64_t(pts)
	avpkt.dts = C.int64_t(pts)
	// Set the keyframe flag if this is an IDR frame. This is needed to make sure the
	// muxer knows it is a keyframe and is safe to start a new segment.
	if isIDR {
		avpkt.flags |= C.AV_PKT_FLAG_KEY
	}
	// Write the packet to the output file.
	ret := C.av_interleaved_write_frame(rs.outCtx, avpkt)
	if ret < 0 {
		return fmt.Errorf("failed to write frame: %s", ffmpegError(ret))
	}
	return nil
}

// close closes the segmenter and writes the trailer to prevent corruption
// when exiting early in the middle of a segment.
func (rs *rawSegmenter) close() {
	rs.outCtxMu.Lock()
	defer rs.outCtxMu.Unlock()
	if rs.outCtx == nil {
		return
	}
	ret := C.av_write_trailer(rs.outCtx)
	if ret < 0 {
		rs.logger.Errorf("failed to write trailer", "error", ffmpegError(ret))
	}
	C.avformat_free_context(rs.outCtx)
	rs.outCtx = nil
}

/*
	aligned(8) class AVCDecoderConfigurationRecord {
		   unsigned int(8) configurationVersion = 1;
		   unsigned int(8) AVCProfileIndication;
		   unsigned int(8) profile_compatibility;
		   unsigned int(8) AVCLevelIndication;
		   bit(6) reserved = ‘111111’b;
		   unsigned int(2) lengthSizeMinusOne;
		   bit(3) reserved = ‘111’b;
		   unsigned int(5) numOfSequenceParameterSets;
		   for (i=0; i< numOfSequenceParameterSets;  i++) {
		      unsigned int(16) sequenceParameterSetLength ;
		  bit(8*sequenceParameterSetLength) sequenceParameterSetNALUnit;
		 }
		   unsigned int(8) numOfPictureParameterSets;
		   for (i=0; i< numOfPictureParameterSets;  i++) {
		  unsigned int(16) pictureParameterSetLength;
		  bit(8*pictureParameterSetLength) pictureParameterSetNALUnit;
		 }
		}
*/
// buildAVCExtradata builds the AVCDecoderConfigurationRecord extradata from the SPS and PPS data.
// The SPS and PPS data are expected to be packed into the format listed above.
func buildAVCExtradata(sps, pps []byte) ([]byte, error) {
	if len(sps) < 4 || len(pps) < 1 {
		return nil, errors.New("invalid SPS/PPS data")
	}
	extradata := []byte{}
	// configurationVersion
	extradata = append(extradata, 1)
	// AVCProfileIndication, profile_compatibility, AVCLevelIndication (from SPS)
	extradata = append(extradata, sps[1], sps[2], sps[3])
	// 6 bits reserved (111111) + 2 bits lengthSizeMinusOne (3 for 4 bytes)
	//nolint:mnd
	extradata = append(extradata, 0xFF)
	// 3 bits reserved (111) + 5 bits numOfSequenceParameterSets (usually 1)
	//nolint:mnd
	extradata = append(extradata, 0xE1)
	// SPS length (2 bytes big-endian)
	spsLen := uint16(len(sps))
	//nolint:mnd
	extradata = append(extradata, byte(spsLen>>8), byte(spsLen&0xff))
	// SPS data
	extradata = append(extradata, sps...)
	// Number of Picture Parameter Sets (usually 1)
	extradata = append(extradata, 1)
	// PPS length (2 bytes big-endian)
	ppsLen := uint16(len(pps))
	//nolint:mnd
	extradata = append(extradata, byte(ppsLen>>8), byte(ppsLen&0xff))
	// PPS data
	extradata = append(extradata, pps...)

	// hexdump := hex.Dump(extradata)
	// fmt.Println("extradata: \n", hexdump)
	return extradata, nil
}
