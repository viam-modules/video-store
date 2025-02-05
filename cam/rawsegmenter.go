package videostore

/*
#cgo pkg-config: libavutil libavcodec libavformat libswscale
#include <libavfilter/avfilter.h>
#include <libavutil/avutil.h>
#include <libavformat/avformat.h>
#include <libavutil/dict.h>
#include <libavutil/error.h>
#include <libavcodec/avcodec.h>
*/
import "C"

import (
	"encoding/hex"
	"errors"
	"fmt"
	"unsafe"

	"go.viam.com/rdk/logging"
)

type RawSegmenter struct {
	logger      logging.Logger
	storagePath string
	outCtx      *C.AVFormatContext
}

func NewRawSegmenter(
	logger logging.Logger,
	storagePath string,
) (*RawSegmenter, error) {
	s := &RawSegmenter{
		logger:      logger,
		storagePath: storagePath,
	}
	err := createDir(s.storagePath)
	if err != nil {
		return nil, err
	}
	s.logger.Info("created segmenter")

	return s, nil
}

func (rs *RawSegmenter) Init(codecID C.enum_AVCodecID, sps, pps []byte) error {
	fmt.Println("Init segmenter")
	rs.logger.Info("setting up output ctx")
	// Allocate output context for segmenter. The "segment" format is a special format
	// that allows for segmenting output files. The output pattern is a strftime pattern
	// that specifies the output file name. The pattern is set to the current time.
	outputPatternCStr := C.CString(rs.storagePath + "/" + outputPattern)
	defer C.free(unsafe.Pointer(outputPatternCStr))
	formatName := C.CString("segment")
	defer C.free(unsafe.Pointer(formatName))
	// log the output pattern
	fmt.Println("outputPatternCStr: ", C.GoString(outputPatternCStr))
	var fmtCtx *C.AVFormatContext
	ret := C.avformat_alloc_output_context2(&fmtCtx, nil, formatName, outputPatternCStr)
	if ret < 0 {
		return fmt.Errorf("failed to allocate output context: %s", ffmpegError(ret))
	}
	rs.outCtx = fmtCtx

	rs.logger.Info("setting up stream")
	// Create new stream for the output context
	stream := C.avformat_new_stream(fmtCtx, nil)
	if stream == nil {
		return errors.New("failed to allocate stream")
	}
	stream.id = C.int(fmtCtx.nb_streams) - 1
	stream.time_base.num = 1
	stream.time_base.den = 1000

	rs.logger.Info("setting up codec ctx")
	// Set the codec parameters for the output stream
	// TODO(seanp): What should the time base be?
	codec := C.avcodec_find_encoder(codecID)
	if codec == nil {
		return errors.New("failed to find codec")
	}

	codecCtx := C.avcodec_alloc_context3(codec)
	if codecCtx == nil {
		return errors.New("failed to allocate codec context")
	}
	codecCtx.width = 704
	codecCtx.height = 480
	codecCtx.pix_fmt = C.AV_PIX_FMT_YUV420P
	codecCtx.time_base.num = 1
	// codecCtx.time_base.den = 90000
	codecCtx.time_base.den = 1000
	// codecCtx.pix_fmt = C.AV_PIX_FMT_YUV420P

	// Set up the extradata for the codec context
	extradata, err := buildAVCCExtradata(sps, pps)
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

	rs.logger.Info("copying codec parameters")
	// Copy the codec parameters from the input stream to the output stream
	if ret := C.avcodec_parameters_from_context(stream.codecpar, codecCtx); ret < 0 {
		return fmt.Errorf("failed to copy codec parameters: %s", ffmpegError(ret))
	}

	rs.logger.Info("setting up segmenting parameters")
	// Set up segmenting parameters
	// segmentLengthCStr := C.CString(strconv.Itoa(10))
	segmentLengthCStr := C.CString("10")
	segmentFormatCStr := C.CString("mp4")
	resetTimestampsCStr := C.CString("1")
	breakNonKeyFramesCStr := C.CString("1")
	strftimeCStr := C.CString("1")
	defer func() {
		C.free(unsafe.Pointer(segmentLengthCStr))
		C.free(unsafe.Pointer(segmentFormatCStr))
		C.free(unsafe.Pointer(resetTimestampsCStr))
		C.free(unsafe.Pointer(breakNonKeyFramesCStr))
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
	// TODO(seanp): Allowing this could cause flakey playback. Remove if not needed.
	// Or, fix by adding keyframe forces on the encoder side
	ret = C.av_dict_set(&opts, C.CString("break_non_keyframes"), breakNonKeyFramesCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set break_non_keyframes: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("strftime"), strftimeCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set strftime: %s", ffmpegError(ret))
	}

	rs.logger.Info("writing header")
	// Open the output file for writing
	ret = C.avformat_write_header(fmtCtx, &opts)
	if ret < 0 {
		return fmt.Errorf("failed to write header: %s", ffmpegError(ret))
	}

	// need to reset stream time base to 1/1000
	stream.time_base.num = 1
	stream.time_base.den = 1000

	return nil
}

// func (rs *RawSegmenter) WritePacket(pkt *rtp.Packet, pts int64) error {
// 	rs.logger.Info("creating av packet")
// 	// Turn the gortsplib packet into a AVPacket
// 	avpkt := C.av_packet_alloc()
// 	if avpkt == nil {
// 		return errors.New("failed to allocate AVPacket")
// 	}
// 	defer C.av_packet_free(&avpkt)

// 	// Set the packet data and size
// 	avpkt.data = (*C.uint8_t)(unsafe.Pointer(&pkt.Payload[0]))
// 	avpkt.size = C.int(len(pkt.Payload))
// 	if avpkt.size == 0 {
// 		return errors.New("empty packet")
// 	}
// 	if avpkt.data == nil {
// 		return errors.New("nil packet data")
// 	}
// 	// rs.logger.Info("setting pts: ", pkt.Timestamp)
// 	// convertedTimestamp := pkt.Timestamp / 1500
// 	// avpkt.pts = C.int64_t(convertedTimestamp)
// 	// avpkt.dts = C.int64_t(convertedTimestamp)
// 	avpkt.pts = C.int64_t(pts)
// 	avpkt.dts = C.int64_t(pts)

// 	rs.logger.Info("writing packet: ", len(pkt.Payload), pkt.Timestamp, pts)
// 	// Write the packet to the output file
// 	ret := C.av_interleaved_write_frame(rs.outCtx, avpkt)
// 	if ret < 0 {
// 		return fmt.Errorf("failed to write frame: %s", ffmpegError(ret))
// 	}
// 	rs.logger.Info("wrote packet")
// 	return nil
// }

func (rs *RawSegmenter) WritePacket(payload []byte, pts int64) error {
	rs.logger.Infof("creating av packet: %d, pts: %d", len(payload), pts)
	// Turn the gortsplib packet into a AVPacket
	avpkt := C.av_packet_alloc()
	if avpkt == nil {
		return errors.New("failed to allocate AVPacket")
	}
	defer C.av_packet_free(&avpkt)

	// Set the packet data and size
	// avpkt.data = (*C.uint8_t)(unsafe.Pointer(&payload[0]))
	// avpkt.size = C.int(len(payload))
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
	// rs.logger.Info("setting pts: ", pkt.Timestamp)
	// convertedTimestamp := pkt.Timestamp / 1500
	// avpkt.pts = C.int64_t(convertedTimestamp)
	// avpkt.dts = C.int64_t(convertedTimestamp)
	avpkt.pts = C.int64_t(pts)
	avpkt.dts = C.int64_t(pts)

	// Write the packet to the output file
	ret := C.av_interleaved_write_frame(rs.outCtx, avpkt)
	if ret < 0 {
		return fmt.Errorf("failed to write frame: %s", ffmpegError(ret))
	}
	return nil
}

func buildAVCCExtradata(sps, pps []byte) ([]byte, error) {
	if len(sps) < 4 || len(pps) < 1 {
		return nil, fmt.Errorf("invalid SPS/PPS data")
	}
	fmt.Println("length of sps: ", len(sps))
	fmt.Println("length of pps: ", len(pps))
	extradata := []byte{}
	// configurationVersion
	extradata = append(extradata, 1)
	// AVCProfileIndication, profile_compatibility, AVCLevelIndication (from SPS)
	extradata = append(extradata, sps[1], sps[2], sps[3])
	// 6 bits reserved (111111) + 2 bits lengthSizeMinusOne (3 for 4 bytes)
	extradata = append(extradata, 0xFF)
	// 3 bits reserved (111) + 5 bits numOfSequenceParameterSets (usually 1)
	extradata = append(extradata, 0xE1)
	// SPS length (2 bytes big-endian)
	spsLen := uint16(len(sps))
	extradata = append(extradata, byte(spsLen>>8), byte(spsLen&0xff))
	// SPS data
	extradata = append(extradata, sps...)
	// Number of Picture Parameter Sets (usually 1)
	extradata = append(extradata, 1)
	// PPS length (2 bytes big-endian)
	ppsLen := uint16(len(pps))
	extradata = append(extradata, byte(ppsLen>>8), byte(ppsLen&0xff))
	// PPS data
	extradata = append(extradata, pps...)

	// fmpt println hex dump here
	hexdump := hex.Dump(extradata)
	fmt.Println("extradata: \n", hexdump)
	return extradata, nil
}
