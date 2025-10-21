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

int video_store_raw_seg_init(struct raw_seg **ppRS,     // OUT
                             const int segmentSeconds,  // IN
                             const char *outputPattern, // IN
                             const int width,           // IN
                             const int height,          // IN
                             const AVCodec *codec       // IN
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

  // fMP4 (fragmented MP4) options
  //
  // [moov(init)] [moof][mdat] [moof][mdat] ...
  //
  // - frag_keyframe: start a new fragment (moof+mdat) at each keyframe so segments
  //   align with keyframe boundaries.
  // - default_base_moof: set the base_data_offset in the moof box to 0
  //
  // NOTE: We do NOT include empty_moov option because
  // we want the params lookup to be fast for the indexer and matcher.
  //
  // - We still enforce that each segment file has consistent video params.
  // - The indexer and matcher can use avformat_find_stream_info
  //   to simply read the 'moov' box at the start of each segment.
  ret = av_dict_set(&opts, "segment_format_options", "movflags=frag_keyframe+default_base_moof", 0);

  if (ret < 0) {
      av_log(NULL, AV_LOG_ERROR,
            "video_store_raw_seg_init failed to set segment_format_options for fmp4\n");
      goto cleanup;
  }

  // Open the output file for writing
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
                                  const int height          // IN
) {
  const struct AVCodec *codec = avcodec_find_decoder(AV_CODEC_ID_H264);
  if (codec == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to find codec\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }
  return video_store_raw_seg_init(ppRS, segmentSeconds, outputPattern, width,
                                  height, codec);
}

int video_store_raw_seg_init_h265(struct raw_seg **ppRS,     // OUT
                                  const int segmentSeconds,  // IN
                                  const char *outputPattern, // IN
                                  const int width,           // IN
                                  const int height          // IN
) {
  const struct AVCodec *codec = avcodec_find_decoder(AV_CODEC_ID_H265);
  if (codec == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h265 failed to find codec\n");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }
  return video_store_raw_seg_init(ppRS, segmentSeconds, outputPattern, width,
                                  height, codec);
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
