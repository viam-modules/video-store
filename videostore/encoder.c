#include "encoder.h"
#include "libavcodec/avcodec.h"
#include "libavcodec/packet.h"
#include "libavformat/avformat.h"
#include "libavutil/frame.h"
#include "libavutil/log.h"
#include "libavutil/rational.h"
#include <stdint.h>
// BEGIN internal functions
int setup_encoder_segmenter(struct video_store_h264_encoder *e, // OUT
                            const int width,                    // IN
                            const int height                    // IN

) {
  int ret = VIDEO_STORE_ENCODER_RESP_ERROR;

  AVCodecContext *encoderCtx = NULL;
  AVFormatContext *segmenterCtx = NULL;
  AVCodecParameters *codecParams = NULL;
  AVDictionary *encoderOpts = NULL;
  AVDictionary *segmenterOpts = NULL;

  if (width <= 0 || height <= 0) {
    av_log(
        NULL, AV_LOG_ERROR,
        "setup_encoder_segmenter width and height must be greater than zero\n");
    goto cleanup;
  }

  encoderCtx = avcodec_alloc_context3(e->encoderCodec);
  if (encoderCtx == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to allocate H264 context\n");
    goto cleanup;
  }
  encoderCtx->bit_rate = e->bitrate;
  encoderCtx->pix_fmt = AV_PIX_FMT_YUV420P;
  encoderCtx->width = width;
  encoderCtx->height = height;
  // TODO(seanp): Do we want b frames? This could make it more complicated to
  // split clips.
  encoderCtx->max_b_frames = 0;
  encoderCtx->time_base = (AVRational){.num = 1, .den = e->targetFrameRate};

  ret = av_dict_set(&encoderOpts, "preset", e->preset, 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to encoder preset opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = av_dict_set(&encoderOpts, "tune", "zerolatency", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to set encoder tune opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = avcodec_open2(encoderCtx, e->encoderCodec, &encoderOpts);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to open the H264 codec "
           "context: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  // END video encoder
  // BEGIN Segmenter
  ret = avformat_alloc_output_context2(&segmenterCtx, NULL, "segment",
                                       e->outputPattern);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to allocate output context : "
           "%s\n ",
           av_err2str(ret));
    goto cleanup;
  }

  AVStream *segmenterStream = avformat_new_stream(segmenterCtx, NULL);
  if (segmenterStream == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to create new stream\n");
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }
  codecParams = avcodec_parameters_alloc();

  ret = avcodec_parameters_from_context(codecParams, encoderCtx);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to allocate codec parameters: "
           "%s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = avcodec_parameters_copy(segmenterStream->codecpar, codecParams);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to copy codec parameters: %s\n",
           av_err2str(ret));
    goto cleanup;
  }
  char stackSegmentSecondsStr[30];
  snprintf(stackSegmentSecondsStr, sizeof(stackSegmentSecondsStr), "%d",
           e->segmentSeconds);
  ret = av_dict_set(&segmenterOpts, "segment_time", stackSegmentSecondsStr, 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to set segmenter segment_time "
           "opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = av_dict_set(&segmenterOpts, "segment_format", "mp4", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to set segmenter segment_format "
           "opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = av_dict_set(&segmenterOpts, "reset_timestamps", "1", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to set segmenter "
           "reset_timestamps "
           "opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = av_dict_set(&segmenterOpts, "break_non_keyframes", "1", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to set segmenter "
           "break_non_keyframes "
           "opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = av_dict_set(&segmenterOpts, "strftime", "1", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to set segmenter "
           "strftime "
           "opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = avformat_write_header(segmenterCtx, &segmenterOpts);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "setup_encoder_segmenter failed to avformat_write_header %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  // NOTE: (Nick S) this needs to be set so that the segmenter knows how to
  // correctly interpret the pts and dts of the packets that come out of the
  // encoder
  segmenterStream->time_base = encoderCtx->time_base;
  // encoder
  e->encoderCtx = encoderCtx;

  // segmenter
  e->segmenterCtx = segmenterCtx;
  /* e->segmenterStream = segmenterStream; */
  ret = VIDEO_STORE_ENCODER_RESP_OK;
cleanup:
  if (ret != VIDEO_STORE_ENCODER_RESP_OK) {
    av_log(NULL, AV_LOG_ERROR, "setup_encoder_segmenter doing cleanup\n");
    // error cleanup
    if (segmenterCtx != NULL) {
      avformat_free_context(segmenterCtx);
    }

    if (encoderCtx != NULL) {
      avcodec_free_context(&encoderCtx);
    }
  }

  // normal cleanup
  if (codecParams != NULL) {
    avcodec_parameters_free(&codecParams);
  }
  if (encoderOpts != NULL) {
    av_dict_free(&encoderOpts);
  }
  if (segmenterOpts != NULL) {
    av_dict_free(&segmenterOpts);
  }
  return ret;
}

void close_encoder_segmenter(struct video_store_h264_encoder *e) {
  // encoder
  if (e->encoderCtx != NULL) {
    avcodec_free_context(&e->encoderCtx);
    e->encoderCtx = NULL;
  }

  // segmenter
  if (e->segmenterCtx != NULL) {
    int ret = av_write_trailer(e->segmenterCtx);
    if (ret != 0) {
      av_log(NULL, AV_LOG_ERROR,
             "close_encoder_segmenter failed to write trailer: %s\n",
             av_err2str(ret));
    }
    avformat_free_context(e->segmenterCtx);
    e->segmenterCtx = NULL;
  }
  e->frameCount = 0;
}

int video_store_h264_encoder_init(struct video_store_h264_encoder **ppE, // OUT
                                  const int segmentSeconds,              // IN
                                  const char *outputPattern,             // IN
                                  const int64_t bitrate,                 // IN
                                  const int targetFrameRate,             // IN
                                  const char *preset                     // IN

) {
  struct video_store_h264_encoder *e = NULL;
  AVFrame *decoderFrame = NULL;
  AVCodecContext *decoderCtx = NULL;
  AVPacket *encoderPkt = NULL;

  int ret = VIDEO_STORE_ENCODER_RESP_ERROR;

  // calloc so that the memory is zeroed which is a safer default
  e = (struct video_store_h264_encoder *)calloc(
      1, sizeof(struct video_store_h264_encoder));
  if (e == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed allocate a "
           "video_store_h264_encoder\n");
    goto cleanup;
  }

  // BEGIN frame decoder
  const AVCodec *frameCodec = avcodec_find_decoder(AV_CODEC_ID_MJPEG);
  if (frameCodec == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to find JPEG decoder\n");
    goto cleanup;
  }

  decoderCtx = avcodec_alloc_context3(frameCodec);
  if (decoderCtx == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to allocate JPEG context\n");
    goto cleanup;
  }

  decoderCtx->pix_fmt = AV_PIX_FMT_YUV420P;
  ret = avcodec_open2(decoderCtx, frameCodec, NULL);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to open the JPEG codec "
           "context: %s\n",
           av_err2str(ret));
    goto cleanup;
  };

  decoderFrame = av_frame_alloc();
  if (decoderFrame == NULL) {
    av_log(
        NULL, AV_LOG_ERROR,
        "video_store_h264_encoder_init failed to allocate destination frame\n");
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }
  // END frame decoder

  // BEGIN video encoder
  encoderPkt = av_packet_alloc();
  if (encoderPkt == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_write failed to av_packet_alloc\n");
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }

  const AVCodec *encoderCodec = avcodec_find_encoder(AV_CODEC_ID_H264);
  if (encoderCodec == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to find H264 encoder\n");
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }

  char *presetStr = (char *)calloc(MAX_PRESET_SIZE, sizeof(char));
  if (presetStr == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed allocate a "
           "presetStr\n");
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }
  char *outputPatternStr =
      (char *)calloc(MAX_OUTPUT_PATTERN_SIZE, sizeof(char));
  if (outputPatternStr == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed allocate a "
           "outputPatternStr\n");
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }

  snprintf(presetStr, MAX_PRESET_SIZE, "%s", preset);
  snprintf(outputPatternStr, MAX_OUTPUT_PATTERN_SIZE, "%s", outputPattern);

  e->decoderCtx = decoderCtx;
  e->decoderFrame = decoderFrame;
  e->encoderCodec = encoderCodec;
  e->segmentSeconds = segmentSeconds;
  e->bitrate = bitrate;
  e->targetFrameRate = targetFrameRate;
  e->preset = presetStr;
  e->outputPattern = outputPatternStr;
  e->encoderPkt = encoderPkt;
  e->frameCount = 0;

  *ppE = e;
  ret = VIDEO_STORE_ENCODER_RESP_OK;
  // END Success
cleanup:
  if (ret != VIDEO_STORE_ENCODER_RESP_OK) {
    av_log(NULL, AV_LOG_ERROR, "video_store_h264_encoder_init doing cleanup\n");
    // error cleanup
    if (e != NULL) {
      free(e);
    }
    if (decoderCtx != NULL) {
      avcodec_free_context(&decoderCtx);
    }
    if (decoderFrame != NULL) {
      av_frame_free(&decoderFrame);
    }
    if (presetStr != NULL) {
      free((void *)presetStr);
    }
    if (outputPatternStr != NULL) {
      free((void *)outputPatternStr);
    }
  }

  return ret;
}
// END internal functions

// BEGIN C API Implementation
int video_store_h264_encoder_write(struct video_store_h264_encoder *e, // IN
                                   void *payload,                      // IN
                                   size_t payloadSize                  // IN
) {
  if (e == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_write received null "
           "video_store_h264_encoder pointer");
    return VIDEO_STORE_ENCODER_RESP_ERROR;
  }

  if (payload == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_write received null "
           "payload pointer");
    return VIDEO_STORE_ENCODER_RESP_ERROR;
  }
  int ret = VIDEO_STORE_ENCODER_RESP_ERROR;
  AVPacket pkt = {
      .data = payload,
      .size = (int)payloadSize,
  };
  ret = avcodec_send_packet(e->decoderCtx, &pkt);
  if (ret != 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_write failed to avcodec_send_packet %s\n",
           av_err2str(ret));
    goto cleanup;
  }
  // Receive frame will allocate the frame buffer so we do not need to
  // manually call av_frame_get_buffer.
  ret = avcodec_receive_frame(e->decoderCtx, e->decoderFrame);
  if (ret != 0) {
    av_log(
        NULL, AV_LOG_ERROR,
        "video_store_h264_encoder_write failed to avcodec_receive_frame %s\n",
        av_err2str(ret));
    goto cleanup;
  }

  // if the width & height have changed, close the encoder and segmenter
  // and set them to null
  if (e->encoderCtx != NULL &&
      (e->decoderFrame->width != e->encoderCtx->width ||
       e->decoderFrame->height != e->encoderCtx->height)) {
    close_encoder_segmenter(e);
  }

  // If the encoder is null and we have a valid frame, set up the encoder &
  // segmenter
  if (e->encoderCtx == NULL) {
    ret = setup_encoder_segmenter(e, e->decoderFrame->width,
                                  e->decoderFrame->height);
    if (ret != VIDEO_STORE_ENCODER_RESP_OK) {
      av_log(NULL, AV_LOG_ERROR,
             "video_store_h264_encoder_write failed to setup_encoder_segmenter "
             "\n");
      goto cleanup;
    }
  }
  e->decoderFrame->pts = e->frameCount;
  e->decoderFrame->pkt_dts = e->decoderFrame->pts;

  // Manually force keyframes every second, removing the need to rely on
  // gop_size or other encoder settings. This is necessary for the  segmenter
  // to split the video files at keyframe boundaries.
  // if it has been a second or more, add an iframe
  if (e->decoderFrame->pts % e->encoderCtx->time_base.den == 0) {
    e->decoderFrame->flags |= AV_FRAME_FLAG_KEY;
    e->decoderFrame->pict_type = AV_PICTURE_TYPE_I;
  }

  ret = avcodec_send_frame(e->encoderCtx, e->decoderFrame);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_write failed to avcodec_send_frame %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = avcodec_receive_packet(e->encoderCtx, e->encoderPkt);
  if (ret < 0) {
    av_log(
        NULL, AV_LOG_ERROR,
        "video_store_h264_encoder_write failed to avcodec_receive_packet %s\n",
        av_err2str(ret));
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }
  av_log(NULL, AV_LOG_INFO,
         "avcodec_receive_packet: OUT: e->encoderPkt->pts: %lld, "
         "e->encoderPkt->dts: %lld, e->encoderPkt->stream_index: %d\n",
         e->encoderPkt->pts, e->encoderPkt->dts, e->encoderPkt->stream_index);

  ret = av_interleaved_write_frame(e->segmenterCtx, e->encoderPkt);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_write failed to "
           "av_interleaved_write_frame %s\n",
           av_err2str(ret));
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }
  e->frameCount++;

cleanup:
  /* av_packet_unref(e->decoderPkt); */
  av_packet_unref(e->encoderPkt);
  return ret;
}

int video_store_h264_encoder_close(struct video_store_h264_encoder **ppE // OUT
) {
  if (ppE == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_close called with null "
           "video_store_h264_encoder "
           "**ppE\n");
    return VIDEO_STORE_ENCODER_RESP_ERROR;
  }

  if (*ppE == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_close called with null "
           "video_store_h264_encoder "
           "*ppE\n");
    return VIDEO_STORE_ENCODER_RESP_ERROR;
  }
  close_encoder_segmenter(*ppE);
  av_packet_free(&(*ppE)->encoderPkt);

  // decoder
  av_frame_free(&(*ppE)->decoderFrame);
  /* av_packet_free(&(*ppE)->decoderPkt); */
  avcodec_free_context(&(*ppE)->decoderCtx);
  free((void *)(*ppE)->outputPattern);
  free((void *)(*ppE)->preset);
  (*ppE)->outputPattern = NULL;
  (*ppE)->preset = NULL;
  // struct
  free(*ppE);
  *ppE = NULL;
  return VIDEO_STORE_ENCODER_RESP_OK;
}
// END C API Implementation
