#include "encoder.h"
#include "libavcodec/avcodec.h"
#include "libavformat/avformat.h"
#include "libavutil/log.h"
int video_store_h264_encoder_init(struct video_store_h264_encoder **ppE, // OUT
                                  const int segmentSeconds,              // IN
                                  const char *outputPattern,             // IN
                                  const int width,                       // IN
                                  const int height,                      // IN
                                  const int64_t bitrate, const int frameRate,
                                  const char *preset) {
  struct video_store_h264_encoder *e = NULL;
  AVFrame *destFrame = NULL;
  AVFrame *srcFrame = NULL;
  AVCodecContext *jpegCodecCtx = NULL;
  AVCodecContext *encoderCtx = NULL;
  AVFormatContext *segmenterCtx = NULL;
  AVCodecParameters *codecParams = NULL;
  AVDictionary *encoderOpts = NULL;
  AVDictionary *segmenterOpts = NULL;
  int ret = VIDEO_STORE_ENCODER_RESP_ERROR;

  // calloc so that the memory is zeroed which is a safer default
  e = (struct video_store_h264_encoder *)calloc(
      1, sizeof(struct video_store_h264_encoder));
  if (e == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed allocate a raw_seg_h264\n");
    goto cleanup;
  }

  // BEGIN frame decoder
  const AVCodec *frameCodec = avcodec_find_decoder(AV_CODEC_ID_MJPEG);
  if (frameCodec == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to find JPEG decoder\n");
    goto cleanup;
  }

  jpegCodecCtx = avcodec_alloc_context3(frameCodec);
  if (jpegCodecCtx == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to allocate JPEG context\n");
    goto cleanup;
  }

  jpegCodecCtx->pix_fmt = AV_PIX_FMT_YUV420P;
  ret = avcodec_open2(jpegCodecCtx, frameCodec, NULL);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to open the JPEG codec "
           "context: %s\n",
           av_err2str(ret));
    goto cleanup;
  };

  destFrame = av_frame_alloc();
  if (destFrame == NULL) {
    av_log(
        NULL, AV_LOG_ERROR,
        "video_store_h264_encoder_init failed to allocate destination frame\n");
    goto cleanup;
  }
  // END frame decoder

  // BEGIN video encoder
  const AVCodec *encoderCodec = avcodec_find_encoder(AV_CODEC_ID_H264);
  if (encoderCodec == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to find H264 encoder\n");
    goto cleanup;
  }
  encoderCtx = avcodec_alloc_context3(encoderCodec);
  if (encoderCtx == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to allocate H264 context\n");
    goto cleanup;
  }
  encoderCtx->bit_rate = bitrate;
  encoderCtx->pix_fmt = AV_PIX_FMT_YUV420P;
  encoderCtx->time_base.num = 1;
  encoderCtx->time_base.num = frameRate;
  encoderCtx->width = width;
  encoderCtx->height = height;
  // TODO(seanp): Do we want b frames? This could make it more complicated to
  // split clips.
  encoderCtx->max_b_frames = 0;

  ret = av_dict_set(&encoderOpts, "preset", preset, 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to encoder preset opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = av_dict_set(&encoderOpts, "tune", "zerolatency", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to set encoder tune opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = avcodec_open2(encoderCtx, encoderCodec, &encoderOpts);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to open the H264 codec "
           "context: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  srcFrame = av_frame_alloc();
  if (srcFrame == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to allocate source frame\n");
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }
  srcFrame->width = encoderCtx->width;
  srcFrame->height = encoderCtx->height;
  srcFrame->format = encoderCtx->pix_fmt;

  // END video encoder
  // BEGIN Segmenter
  ret = avformat_alloc_output_context2(&segmenterCtx, NULL, "segment",
                                       outputPattern);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to allocate output context : "
           "%s\n ",
           av_err2str(ret));
    goto cleanup;
  }

  AVStream *segmenterStream = avformat_new_stream(segmenterCtx, NULL);
  if (segmenterStream == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to create new stream\n");
    goto cleanup;
  }
  /* // NOTE: (NICK): Why do we need to do this? Isn't this always */
  /* going to be */
  // negative 1?
  segmenterStream->id = (int)(segmenterCtx->nb_streams) - 1;
  segmenterStream->time_base = encoderCtx->time_base;
  codecParams = avcodec_parameters_alloc();

  ret = avcodec_parameters_from_context(codecParams, encoderCtx);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to allocate codec parameters: "
           "%s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = avcodec_parameters_copy(segmenterStream->codecpar, codecParams);
  if (ret < 0) {
    av_log(
        NULL, AV_LOG_ERROR,
        "video_store_h264_encoder_init failed to copy codec parameters: %s\n",
        av_err2str(ret));
    goto cleanup;
  }
  char stackSegmentSecondsStr[30];
  snprintf(stackSegmentSecondsStr, sizeof(stackSegmentSecondsStr), "%d",
           segmentSeconds);
  ret = av_dict_set(&segmenterOpts, "segment_time", stackSegmentSecondsStr, 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to set segmenter segment_time "
           "opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = av_dict_set(&segmenterOpts, "segment_format", "mp4", 0);
  if (ret < 0) {
    av_log(
        NULL, AV_LOG_ERROR,
        "video_store_h264_encoder_init failed to set segmenter segment_format "
        "opt: %s\n",
        av_err2str(ret));
    goto cleanup;
  }

  ret = av_dict_set(&segmenterOpts, "reset_timestamps", "1", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to set segmenter "
           "reset_timestamps "
           "opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = av_dict_set(&segmenterOpts, "break_non_keyframes", "1", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to set segmenter "
           "break_non_keyframes "
           "opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = av_dict_set(&segmenterOpts, "strftime", "1", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to set segmenter "
           "strftime "
           "opt: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = avformat_write_header(segmenterCtx, &segmenterOpts);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_init failed to avformat_write_header %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  // Writing header overwrites the time_base, so we need to reset it.
  // TODO(seanp): Figure out why this is necessary.
  segmenterStream->time_base = encoderCtx->time_base;
  segmenterStream->id = (int)(segmenterCtx->nb_streams) - 1;
  // END Segmenter

  // BEGIN Success
  // decoder
  e->decoderCtx = jpegCodecCtx;
  e->decoderFrame = destFrame;

  // encoder
  e->encoderCtx = encoderCtx;
  e->encoderFrame = srcFrame;

  // segmenter
  e->segmenterCtx = segmenterCtx;
  e->segmenterStream = segmenterStream;

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
    if (jpegCodecCtx != NULL) {
      avcodec_free_context(&jpegCodecCtx);
    }
    if (destFrame != NULL) {
      av_frame_free(&destFrame);
    }

    if (srcFrame != NULL) {
      av_frame_free(&srcFrame);
    }

    if (segmenterCtx != NULL) {
      avformat_free_context(segmenterCtx);
    }

    if (encoderCtx != NULL) {
      int tmpRet = avcodec_close(encoderCtx);
      if (tmpRet < 0) {
        av_log(NULL, AV_LOG_ERROR,
               "video_store_h264_encoder_init failed to close codec H264: %s\n",
               av_err2str(tmpRet));
      }
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

int video_store_h264_encoder_frame(struct video_store_h264_encoder *pE, // IN
                                   uint8_t *payload,                    // IN
                                   int payloadSize                      // IN
) {
  // fill a jpeg pkt with the frame bytes
  int ret = VIDEO_STORE_ENCODER_RESP_ERROR;
  AVPacket decoderPkt = {0};
  decoderPkt.data = payload;
  decoderPkt.size = payloadSize;
  AVPacket *encoderPkt = NULL;
  // The mjpeg decoder can figure out width and height from the frame bytes.

  // We don't need to pass width and height to initJPEGDecoder and it can
  // recover from a change in resolution.
  ret = avcodec_send_packet(pE->decoderCtx, &decoderPkt);
  if (ret != 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_frame failed to avcodec_send_packet %s\n",
           av_err2str(ret));
    goto cleanup;
  }
  // Receive frame will allocate the frame buffer so we do not need to
  // manually call av_frame_get_buffer.
  ret = avcodec_receive_frame(pE->decoderCtx, pE->decoderFrame);
  if (ret != 0) {
    av_log(
        NULL, AV_LOG_ERROR,
        "video_store_h264_encoder_frame failed to avcodec_receive_frame %s\n",
        av_err2str(ret));
    goto cleanup;
  }

  // TODO Handle frame size changing

  // Both PTS and DTS times are equal frameCount multiplied by the time_base.
  // This assumes that the processFrame routine is running at the source
  // framerate.
  // TODO(seanp): What happens to playback if frame is dropped?
  //
  // TODO: Nick: This is a bad assumption, make it so that the PTS is just
  // determined by wall clock time
  pE->decoderFrame->pts = pE->frameCount;
  pE->decoderFrame->pkt_dts = pE->frameCount;

  /* // Manually force keyframes every second, removing the need to rely on */
  /* // gop_size or other encoder settings. This is necessary for the segmenter
   */
  /* // to split the video files at keyframe boundaries. */
  if (pE->frameCount % pE->encoderCtx->time_base.den == 0) {
    // TODO: Look into key_frame being deprecated
    pE->decoderFrame->key_frame = 1;
    // TODO: Nick try this
    /* pE->decoderFrame->flags |= AV_FRAME_FLAG_KEY; */
    pE->decoderFrame->pict_type = AV_PICTURE_TYPE_I;
  } else {
    pE->decoderFrame->key_frame = 0;
    pE->decoderFrame->pict_type = AV_PICTURE_TYPE_NONE;
  }

  ret = avcodec_send_frame(pE->encoderCtx, pE->decoderFrame);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_frame failed to avcodec_send_frame %s\n",
           av_err2str(ret));
    goto cleanup;
  }
  // TODO: make it so that only one packet gets allocated
  encoderPkt = av_packet_alloc();
  if (encoderPkt == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_frame failed to av_packet_alloc\n");
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }
  ret = avcodec_receive_packet(pE->encoderCtx, encoderPkt);
  if (ret < 0) {
    av_log(
        NULL, AV_LOG_ERROR,
        "video_store_h264_encoder_frame failed to avcodec_receive_packet %s\n",
        av_err2str(ret));
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }
  pE->frameCount++;

  ret = av_interleaved_write_frame(pE->segmenterCtx, encoderPkt);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_frame failed to "
           "av_interleaved_write_frame %s\n",
           av_err2str(ret));
    ret = VIDEO_STORE_ENCODER_RESP_ERROR;
    goto cleanup;
  }

cleanup:
  av_packet_free(&encoderPkt);
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
  // segmenter
  int ret = av_write_trailer((*ppE)->segmenterCtx);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_close failed to write trailer: %s\n",
           av_err2str(ret));
  }
  avformat_free_context((*ppE)->segmenterCtx);

  // encoder
  ret = avcodec_close((*ppE)->encoderCtx);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_h264_encoder_close failed to close codec H264: %s\n",
           av_err2str(ret));
  }
  av_frame_free(&(*ppE)->encoderFrame);
  avcodec_free_context(&(*ppE)->encoderCtx);

  // decoder
  av_frame_free(&(*ppE)->decoderFrame);
  avcodec_free_context(&(*ppE)->decoderCtx);

  // struct
  free(*ppE);
  *ppE = NULL;
  return VIDEO_STORE_ENCODER_RESP_OK;
}
