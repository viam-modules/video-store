#include "rawsegmenter.h"
#include "libavcodec/packet.h"
#include "libavutil/dict.h"
#include "libavutil/log.h"
#include "libavutil/mem.h"
#include "libavutil/intreadwrite.h"
#include <libavcodec/avcodec.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

// Minimal avcC builder for 1 SPS + 1 PPS. (lengthSizeMinusOne = 3 => 4-byte lengths)
int make_avcC_from_sps_pps(const uint8_t *sps_in, int sps_len_in,
                                  const uint8_t *pps_in, int pps_len_in,
                                  uint8_t **extradata, int *extradata_size) {
    if (!sps_in || sps_len_in < 4 || !pps_in || pps_len_in < 1) return AVERROR_INVALIDDATA;

    uint8_t profile = sps_in[1];
    uint8_t compat  = sps_in[2];
    uint8_t level   = sps_in[3];

    int size = 7 + 2 + sps_len_in + 1 + 2 + pps_len_in;
    uint8_t *p = av_malloc(size + AV_INPUT_BUFFER_PADDING_SIZE);
    if (!p) return AVERROR(ENOMEM);
    memset(p + size, 0, AV_INPUT_BUFFER_PADDING_SIZE);

    int i = 0;
    p[i++] = 1;            // configurationVersion
    p[i++] = profile;      // AVCProfileIndication
    p[i++] = compat;       // profile_compatibility
    p[i++] = level;        // AVCLevelIndication
    p[i++] = 0xFF;         // lengthSizeMinusOne (..0011 => 4-byte NALU lengths)
    p[i++] = 0xE1;         // numOfSPS (1)
    AV_WB16(p + i, sps_len_in); i += 2;
    memcpy(p + i, sps_in, sps_len_in); i += sps_len_in;
    p[i++] = 1;            // numOfPPS (1)
    AV_WB16(p + i, pps_len_in); i += 2;
    memcpy(p + i, pps_in, pps_len_in); i += pps_len_in;

    *extradata = p;
    *extradata_size = i;
    return 0;
}

// append one array entry into hvcC (array_completeness=1, numNalus=1)
static void append_hvcc_array(uint8_t *p, int *idx,
                              uint8_t nal_type,
                              const uint8_t *buf, int len)
{
    if (!buf || len <= 0) return;
    p[(*idx)++] = 0x80 | (nal_type & 0x3F);    // array_completeness=1, nal_unit_type
    AV_WB16(p + *idx, 1);  *idx += 2;          // numNalus = 1
    AV_WB16(p + *idx, len); *idx += 2;         // nalUnitLength
    memcpy(p + *idx, buf, len); *idx += len;   // nalUnit
}

// Very small hvcC builder for 1 VPS + 1 SPS + 1 PPS with nal_unit_length=4.
static int make_hvcC_from_vps_sps_pps(const uint8_t *vps_in, int vps_len_in,
                                      const uint8_t *sps_in, int sps_len_in,
                                      const uint8_t *pps_in, int pps_len_in,
                                      uint8_t **extradata, int *extradata_size)
{
    if (!sps_in || sps_len_in < 4 || !pps_in || pps_len_in < 1)
        return AVERROR_INVALIDDATA;

    // hvcC size estimate: 23 header + arrays (each: 3 + 2 + len)
    int arrays = (vps_in && vps_len_in>0 ? 1 : 0) + 1 + 1; // VPS? + SPS + PPS
    int size = 23 + arrays * 3 + (vps_in ? (2+vps_len_in) : 0) + (2+sps_len_in) + (2+pps_len_in);

    uint8_t *p = av_malloc(size + AV_INPUT_BUFFER_PADDING_SIZE);
    if (!p) return AVERROR(ENOMEM);
    memset(p + size, 0, AV_INPUT_BUFFER_PADDING_SIZE);

    int i = 0;
    p[i++] = 1; // configurationVersion

    // general_profile_space(2) general_tier_flag(1) general_profile_idc(5)
    p[i++] = 0x01; // profile_idc = Main
    // general_profile_compatibility_flags (32)
    AV_WB32(p + i, 0); i += 4;
    // general_constraint_indicator_flags (48)
    AV_WB32(p + i, 0); i += 4;
    AV_WB16(p + i, 0); i += 2;

    p[i++] = 0x1E;

    // min_spatial_segmentation_idc (12) + reserved(4)
    AV_WB16(p + i, 0xF000); i += 2;

    p[i++] = 0xFC;   // parallelismType(2)=0
    p[i++] = 0xFD;   // chromaFormat(2)=1 (4:2:0)
    p[i++] = 0xF8;   // bitDepthLumaMinus8(3)=0
    p[i++] = 0xF8;   // bitDepthChromaMinus8(3)=0

    AV_WB16(p + i, 0); i += 2; // avgFrameRate = 0 (unknown)

    // constantFrameRate(2)=0, numTemporalLayers(3)=0, temporalIdNested(1)=1, lengthSizeMinusOne(2)=3 (=> 4 bytes)
    p[i++] = (0 << 6) | (0 << 3) | (1 << 2) | 3;

    p[i++] = (uint8_t)arrays; // numOfArrays

    if (vps_in && vps_len_in > 0) append_hvcc_array(p, &i, 32, vps_in, vps_len_in); // VPS
    append_hvcc_array(p, &i, 33, sps_in, sps_len_in);                               // SPS
    append_hvcc_array(p, &i, 34, pps_in, pps_len_in);                               // PPS

    *extradata = p;
    *extradata_size = i;
    return 0;
}

int video_store_raw_seg_init(struct raw_seg **ppRS,                 // OUT
                             const int segmentSeconds,              // IN
                             const char *outputPattern,             // IN
                             const int width,                       // IN
                             const int height,                      // IN
                             const AVCodec *codec,                  // IN
                             uint8_t *extradata, int extradata_size // IN
) {
  struct raw_seg *rs = (struct raw_seg *)malloc(sizeof(struct raw_seg));
  if (rs == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init failed allocate a raw_seg_h264\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }
  AVFormatContext *fmtCtx = NULL;
  AVStream *stream = NULL;
  AVCodecContext *codecCtx = NULL;
  AVDictionary *opts = NULL;
  int ret = 0;
  ret = avformat_alloc_output_context2(&fmtCtx, NULL, "segment", outputPattern);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init failed to allocate format context: %s\n",
           av_err2str(ret));
    goto cleanup;
  }
  /* // Create new stream for the output context. */
  stream = avformat_new_stream(fmtCtx, NULL);
  if (stream == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init failed to allocate stream\n");
    ret = VIDEO_STORE_RAW_SEG_RESP_ERROR;
    goto cleanup;
  }
  // NOTE: Nick: Do we need to do this?
  stream->id = (int)(fmtCtx->nb_streams) - 1;

  codecCtx = avcodec_alloc_context3(codec);
  if (codecCtx == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init failed to allocate codec context\n");
    ret = VIDEO_STORE_RAW_SEG_RESP_ERROR;
    goto cleanup;
  }

  codecCtx->width = width;
  codecCtx->height = height;

  ret = avcodec_parameters_from_context(stream->codecpar, codecCtx);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init failed to copy codec parameters\n");
    goto cleanup;
  }
  if (codec->id == AV_CODEC_ID_H265) {
    // this is needed  to make h265 videos playable on apple devices
    // https://trac.ffmpeg.org/wiki/Encode/H.265#FinalCutandApplestuffcompatibility
    // https://stackoverflow.com/questions/50565912/h265-codec-changes-from-hvc1-to-hev1
    stream->codecpar->codec_tag = MKTAG('h', 'v', 'c', '1');
  } else if (codec->id == AV_CODEC_ID_H264) {
    stream->codecpar->codec_tag = MKTAG('a', 'v', 'c', '1');
  }

  char stackSegmentSecondsStr[30];
  snprintf(stackSegmentSecondsStr, sizeof(stackSegmentSecondsStr), "%d",
           segmentSeconds);
  ret = av_dict_set(&opts, "segment_time", stackSegmentSecondsStr, 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init failed to set segment_time\n");
    goto cleanup;
  }

  ret = av_dict_set(&opts, "segment_format", "mp4", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init failed to set segment_format\n");
    goto cleanup;
  }

  ret = av_dict_set(&opts, "reset_timestamps", "1", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init failed to set reset_timestamps\n");
    goto cleanup;
  }

  ret = av_dict_set(&opts, "strftime", "1", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init failed to set strftime\n");
    goto cleanup;
  }

  ret = av_dict_set(&opts, "segment_format_options", "movflags=frag_keyframe+empty_moov+default_base_moof", 0);
  if (ret < 0) {
      av_log(NULL, AV_LOG_ERROR,
            "video_store_raw_seg_init failed to set segment_format_options for fmp4\n");
      goto cleanup;
  }

  // set extradata
  if (extradata && extradata_size > 0) {
    // ensure padding for safety
    uint8_t *ed = av_malloc(extradata_size + AV_INPUT_BUFFER_PADDING_SIZE);
    if (!ed) { ret = AVERROR(ENOMEM); goto cleanup; }
    memcpy(ed, extradata, extradata_size);
    memset(ed + extradata_size, 0, AV_INPUT_BUFFER_PADDING_SIZE);

    stream->codecpar->extradata = ed;
    stream->codecpar->extradata_size = extradata_size;
  }

  /* // Open the output file for writing */
  ret = avformat_write_header(fmtCtx, &opts);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init failed to write header\n");
    goto cleanup;
  }

  rs->outCtx = fmtCtx;
  *ppRS = rs;
  ret = VIDEO_STORE_RAW_SEG_RESP_OK;

cleanup:
  if (ret != VIDEO_STORE_RAW_SEG_RESP_OK) {
    free(rs);
    if (fmtCtx != NULL) {
      avformat_free_context(fmtCtx);
    }
  }
  if (opts != NULL) {
    av_dict_free(&opts);
  }

  if (codecCtx != NULL) {
    avcodec_free_context(&codecCtx);
  }
  return ret;
}

int video_store_raw_seg_init_h264(struct raw_seg **ppRS,     // OUT
                                  const int segmentSeconds,  // IN
                                  const char *outputPattern, // IN
                                  const int width,           // IN
                                  const int height,          // IN
                                  const uint8_t *sps, size_t sps_len,
                                  const uint8_t *pps, size_t pps_len
) {
  const struct AVCodec *codec = avcodec_find_decoder(AV_CODEC_ID_H264);
  if (codec == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to find codec\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }
  // create extradata from sps and pps
  uint8_t *extradata = NULL;
  int extradata_size = 0;
  int ret = make_avcC_from_sps_pps(sps, (int)sps_len, pps, (int)pps_len,
                                   &extradata, &extradata_size);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
            "video_store_raw_seg_init_h264 failed to create extradata from sps and pps\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }
  return video_store_raw_seg_init(ppRS, segmentSeconds, outputPattern, width,
                                  height, codec, extradata, extradata_size);
}

int video_store_raw_seg_init_h265(struct raw_seg **ppRS,     // OUT
                                  const int segmentSeconds,  // IN
                                  const char *outputPattern, // IN
                                  const int width,           // IN
                                  const int height,          // IN
                                  const uint8_t *sps, size_t sps_len,
                                  const uint8_t *pps, size_t pps_len,
                                  const uint8_t *vps, size_t vps_len
) {
  const struct AVCodec *codec = avcodec_find_decoder(AV_CODEC_ID_H265);
  if (codec == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h265 failed to find codec\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }
  // create extradata from sps, pps, and vps
  uint8_t *extradata = NULL;
  int extradata_size = 0;
  int ret = make_hvcC_from_vps_sps_pps(vps, (int)vps_len, sps, (int)sps_len, pps, (int)pps_len,
                                      &extradata, &extradata_size);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
            "video_store_raw_seg_init_h265 failed to create extradata from sps and pps\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }
  return video_store_raw_seg_init(ppRS, segmentSeconds, outputPattern, width,
                                  height, codec, extradata, extradata_size);
}

int video_store_raw_seg_write_packet(struct raw_seg *rs,       // IN
                                     const char *payload,      // IN
                                     const size_t payloadSize, // IN
                                     const int64_t pts,        // IN
                                     const int64_t dts,        // IN
                                     const int isIdr           // IN
) {
  int ret = VIDEO_STORE_RAW_SEG_RESP_ERROR;
  if (payloadSize == 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_packet called with empty payload size\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }

  if (payload == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_packet called with null payload\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }

  if (rs->outCtx == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_packet called before "
           "video_store_raw_seg_write_packet\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }

  AVPacket *pkt = av_packet_alloc();
  if (pkt == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_packet failed to allocate AVPacket\n");
    ret = VIDEO_STORE_RAW_SEG_RESP_ERROR;
    goto cleanup;
  }

  uint8_t *data = av_malloc(payloadSize);
  if (data == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_packet failed to av_malloc\n");
    ret = VIDEO_STORE_RAW_SEG_RESP_ERROR;
    goto cleanup;
  }
  memcpy(data, payload, payloadSize);
  ret = av_packet_from_data(pkt, data, (int)payloadSize);
  if (ret != 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_packet failed to create new "
           "AVPacket from data\n");
    // if av_packet_from_data returned an error then data is not owned by the
    // packet and we need to free it outselves
    av_free(data);
    goto cleanup;
  }

  pkt->size = (int)payloadSize;
  pkt->pts = pts;
  pkt->dts = dts;
  /* // Set the keyframe flag if this is an IDR frame. This is needed to
   * make sure the */
  /* // muxer knows it is a keyframe and is safe to start a new segment. */
  if (isIdr) {
    pkt->flags |= AV_PKT_FLAG_KEY;
  }
  // Write the packet to the output file.
  if ((ret = av_interleaved_write_frame(rs->outCtx, pkt))) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_packet failed to write frame\n");
    goto cleanup;
  }

  ret = VIDEO_STORE_RAW_SEG_RESP_OK;
cleanup:
  if (pkt != NULL) {
    av_packet_unref(pkt);
    av_packet_free(&pkt);
  }
  return ret;
}

int video_store_raw_seg_close(struct raw_seg **ppRS // OUT
) {
  if (ppRS == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_close called with null raw_seg_h264 **ppRS\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }

  if (*ppRS == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_close called with null raw_seg_h264 *ppRS\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }
  int ret = av_write_trailer((*ppRS)->outCtx);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_close called failed to write trailer\n");
    return ret;
  }
  avformat_free_context((*ppRS)->outCtx);
  free(*ppRS);
  *ppRS = NULL;
  return VIDEO_STORE_RAW_SEG_RESP_OK;
}
