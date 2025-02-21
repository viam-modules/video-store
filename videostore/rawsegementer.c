#include "rawsegementer.h"
#include "libavcodec/packet.h"
#include "libavutil/mem.h"
/* #include "libavcodec/packet.h" */
/* #include "libavutil/dict.h" */
/* #include "libavutil/log.h" */
#include <libavcodec/avcodec.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
int video_store_raw_seg_init_h264(struct raw_seg_h264 **ppRS, // OUT
                                  const int segmentSeconds,   // IN
                                  const char *outputPattern,  // IN
                                  const char *extradata,      // IN
                                  const size_t extradataSize, // IN
                                  const int width,            // IN
                                  const int height            // IN
) {
  struct raw_seg_h264 *rs =
      (struct raw_seg_h264 *)malloc(sizeof(struct raw_seg_h264));
  if (rs == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed allocate a raw_seg_h264");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }

  const struct AVCodec *codec = avcodec_find_encoder(AV_CODEC_ID_H264);
  if (codec == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to find codec");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }

  AVFormatContext *fmtCtx = NULL;
  AVStream *stream = NULL;
  AVCodecContext *codecCtx = NULL;
  AVDictionary *opts = NULL;
  int ret = 0;
  ret = avformat_alloc_output_context2(&fmtCtx, NULL, "segment", outputPattern);
  if (ret < 0) {
    av_log(
        NULL, AV_LOG_ERROR,
        "video_store_raw_seg_init_h264 failed to allocate format context: %s\n",
        av_err2str(ret));
    goto cleanup;
  }
  /* // Create new stream for the output context. */
  stream = avformat_new_stream(fmtCtx, NULL);
  if (stream == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to allocate stream");
    ret = VIDEO_STORE_RAW_SEG_RESP_ERROR;
    goto cleanup;
  }
  // NOTE: Nick: Do we need to do this?
  stream->id = (int)(fmtCtx->nb_streams) - 1;

  codecCtx = avcodec_alloc_context3(codec);
  if (codecCtx == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to allocate codec context");
    ret = VIDEO_STORE_RAW_SEG_RESP_ERROR;
    goto cleanup;
  }

  codecCtx->width = width;
  codecCtx->height = height;
  codecCtx->extradata = av_malloc(extradataSize + AV_INPUT_BUFFER_PADDING_SIZE);
  if (codecCtx->extradata == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to allocate extradata");
    ret = VIDEO_STORE_RAW_SEG_RESP_ERROR;
    goto cleanup;
  }
  codecCtx->extradata_size = (int)extradataSize;

  // memcpy can't fail
  memcpy(codecCtx->extradata, extradata, extradataSize);
  /* // Copy the codec parameters from the input stream to the output
   * stream. Thi is equivalent */
  /* // to -c:v copy in ffmpeg cli. This is needed to make sure we do not
   * re-encode the stream. */
  ret = avcodec_parameters_from_context(stream->codecpar, codecCtx);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to copy codec parameters");
    goto cleanup;
  }

  char stackSegmentSecondsStr[30];
  snprintf(stackSegmentSecondsStr, sizeof(stackSegmentSecondsStr), "%d",
           segmentSeconds);
  ret = av_dict_set(&opts, "segment_time", stackSegmentSecondsStr, 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to set segment_time");
    goto cleanup;
  }

  ret = av_dict_set(&opts, "segment_format", "mp4", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to set segment_format");
    goto cleanup;
  }

  ret = av_dict_set(&opts, "reset_timestamps", "1", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to set reset_timestamps");
    goto cleanup;
  }

  ret = av_dict_set(&opts, "strftime", "1", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to set strftime");
    goto cleanup;
  }

  /* // Open the output file for writing */
  ret = avformat_write_header(fmtCtx, &opts);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_init_h264 failed to write header");
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

int video_store_raw_seg_write_h264_packet(struct raw_seg_h264 *rs,  // IN
                                          const char *payload,      // IN
                                          const size_t payloadSize, // IN
                                          const int64_t pts,        // IN
                                          const int isIdr           // IN
) {
  int ret = VIDEO_STORE_RAW_SEG_RESP_ERROR;
  if (payloadSize == 0) {
    av_log(
        NULL, AV_LOG_ERROR,
        "video_store_raw_seg_write_h264_packet called with empty payload size");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }

  if (payload == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_h264_packet called with null payload");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }

  if (rs->outCtx == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_h264_packet called before "
           "video_store_raw_seg_write_h264_packet");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }

  // TODO: move this to init
  AVPacket *pkt = av_packet_alloc();
  if (pkt == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_h264_packet failed to allocate AVPacket");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }
  // after this point, success or failure we need to go to cleanup to free what
  // we have allocated

  pkt->data = av_malloc(payloadSize);
  if (pkt->data == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_h264_packet failed to allocate AVPacket "
           "data");
    goto cleanup;
  }
  memcpy(pkt->data, payload, payloadSize);
  pkt->size = (int)payloadSize;
  pkt->pts = pts;
  pkt->dts = pts;
  /* // Set the keyframe flag if this is an IDR frame. This is needed to
   * make sure the */
  /* // muxer knows it is a keyframe and is safe to start a new segment. */
  if (isIdr) {
    pkt->flags |= AV_PKT_FLAG_KEY;
  }
  // Write the packet to the output file.
  if ((ret = av_interleaved_write_frame(rs->outCtx, pkt))) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_write_h264_packet failed to write frame");
    goto cleanup;
  }

  ret = VIDEO_STORE_RAW_SEG_RESP_OK;
cleanup:
  av_packet_free(&pkt);
  return ret;
}

int video_store_raw_seg_close(struct raw_seg_h264 **ppRS // OUT
) {
  if (ppRS == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_close called with null raw_seg_h264 **ppRS");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }

  if (*ppRS == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_close called with null raw_seg_h264 *ppRS");
    return VIDEO_STORE_RAW_SEG_RESP_ERROR;
  }
  int ret = av_write_trailer((*ppRS)->outCtx);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_raw_seg_close called failed to write trailer");
    return ret;
  }
  avformat_free_context((*ppRS)->outCtx);
  free(*ppRS);
  *ppRS = NULL;
  return VIDEO_STORE_RAW_SEG_RESP_OK;
}
