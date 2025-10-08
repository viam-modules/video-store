#include "concat.h"
#include "libavcodec/packet.h"
#include "libavutil/dict.h"
#include "libavutil/log.h"
#include <libavcodec/bsf.h>
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <string.h>

int video_store_concat(const char *concat_filepath, const char *output_path, const char *container) {
  int ret = VIDEO_STORE_CONCAT_RESP_ERROR;
  AVPacket *packet = av_packet_alloc();
  AVDictionary *options = NULL;
  AVFormatContext *inputCtx = NULL;
  AVFormatContext *outputCtx = NULL;
  int outputPathOpened = 0;
  const AVInputFormat *inputFormat = av_find_input_format("concat");
  if (inputFormat == NULL) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to find input format\n");
    goto cleanup;
  }

  if (packet == NULL) {
    av_log(NULL, AV_LOG_ERROR, "video_store_concat av_packet_alloc failed\n");
    goto cleanup;
  }

  ret = av_dict_set(&options, "safe", "0", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to set option: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  if ((ret = avformat_open_input(&inputCtx, concat_filepath, inputFormat,
                                 &options))) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to open input format: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = avformat_find_stream_info(inputCtx, NULL);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to find stream info: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  ret = avformat_alloc_output_context2(&outputCtx, NULL, "mp4", output_path);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to allocate output context: %s\n",
           av_err2str(ret));
    goto cleanup;
  }
  // TODO(seanp): add comment why we need this
  outputCtx->flags |= AVFMT_GLOBALHEADER;

  for (unsigned int i = 0; i < inputCtx->nb_streams; i++) {
    AVStream *inStream = inputCtx->streams[i];
    AVStream *outStream = avformat_new_stream(outputCtx, NULL);
    if (outStream == NULL) {
      av_log(NULL, AV_LOG_ERROR,
             "video_store_concat failed to create ouput stream for input "
             "stream index %d, %s\n",
             i, av_err2str(ret));
      goto cleanup;
    }

    ret = avcodec_parameters_copy(outStream->codecpar,
                                  inputCtx->streams[i]->codecpar);
    if (ret < 0) {
      av_log(NULL, AV_LOG_ERROR,
             "video_store_concat failed to copy input stream index %d codec "
             "parameters: %s\n",
             i, av_err2str(ret));
      goto cleanup;
    }
    outStream->codecpar->codec_tag = 0;
    outStream->time_base = inStream->time_base;
    av_log(NULL, AV_LOG_DEBUG, "Stream %d type: %d\n", i, outStream->codecpar->codec_type);
    if (outStream->codecpar->codec_type == AVMEDIA_TYPE_VIDEO) {
        av_log(NULL, AV_LOG_DEBUG, "Stream %d extradata size: %d\n", i, outStream->codecpar->extradata_size);
    }
  }

  ret = avio_open(&outputCtx->pb, output_path, AVIO_FLAG_WRITE);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to open output file: %s\n",
           av_err2str(ret));
    goto cleanup;
  }
  outputPathOpened = 1;

  AVDictionary *mux_opts = NULL;
  if (container != NULL && strcmp(container, "fmp4") == 0) {
      av_dict_set(&mux_opts, "movflags", "frag_keyframe+empty_moov+default_base_moof", 0);
  } else {
      av_dict_set(&mux_opts, "movflags", "faststart", 0);
  }

  ret = avformat_write_header(outputCtx, &mux_opts);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to write header: %s\n", av_err2str(ret));
    goto cleanup;
  }

  int frame_ret = 0;
  AVStream *inStream = NULL;
  AVStream *outStream = NULL;
  int64_t prevDts = INT64_MIN;
  while (1) {
    frame_ret = av_read_frame(inputCtx, packet);
    if (frame_ret == AVERROR_EOF) {
      av_packet_unref(packet);
      break;
    };

    if (frame_ret) {
      ret = frame_ret;
      av_log(NULL, AV_LOG_ERROR,
             "video_store_concat failed to read frame: %s\n", av_err2str(ret));
      av_packet_unref(packet);
      goto cleanup;
    }

    if ((packet->flags & AV_PKT_FLAG_DISCARD) == AV_PKT_FLAG_DISCARD) {
      av_packet_unref(packet);
      continue;
    }
    inStream = inputCtx->streams[packet->stream_index];
    outStream = outputCtx->streams[packet->stream_index];
    packet->pts =
        av_rescale_q_rnd(packet->pts, inStream->time_base, outStream->time_base,
                         AV_ROUND_NEAR_INF | AV_ROUND_PASS_MINMAX);
    packet->dts =
        av_rescale_q_rnd(packet->dts, inStream->time_base, outStream->time_base,
                         AV_ROUND_NEAR_INF | AV_ROUND_PASS_MINMAX);
    packet->duration = av_rescale_q_rnd(
        packet->duration, inStream->time_base, outStream->time_base,
        AV_ROUND_NEAR_INF | AV_ROUND_PASS_MINMAX);
    packet->pos = -1;

    if (packet->dts <= prevDts) {
      av_log(NULL, AV_LOG_DEBUG,
             "video_store_concat skipping non monotonically increasing dts: "
             "%ld, prevDts: %ld\n",
             packet->dts, prevDts);
      av_packet_unref(packet);
      continue;
    }
    prevDts = packet->dts;
    if ((ret = av_interleaved_write_frame(outputCtx, packet))) {
      av_packet_unref(packet);
      av_log(NULL, AV_LOG_ERROR,
             "video_store_concat failed to write frame: %s\n", av_err2str(ret));
      goto cleanup;
    }
    av_packet_unref(packet);
  }

  ret = av_write_trailer(outputCtx);
  if (ret < 0)
    av_log(NULL, AV_LOG_ERROR, "write_trailer: %s (%d)\n", av_err2str(ret), ret);

  if (outputCtx && outputCtx->pb && outputCtx->pb->error < 0)
    av_log(NULL, AV_LOG_ERROR, "avio error: %s (%d)\n",
           av_err2str(outputCtx->pb->error), outputCtx->pb->error);


  ret = VIDEO_STORE_CONCAT_RESP_OK;

cleanup:
  av_log(NULL, AV_LOG_DEBUG, "video_store_concat going to cleanup\n");
  if (outputCtx != NULL) {
    if (outputPathOpened) {
      int err = 0;
      if ((err = avio_closep(&outputCtx->pb))) {
        av_log(NULL, AV_LOG_ERROR,
               "video_store_concat failed to close output file: %s\n",
               av_err2str(err));
      };
    }
    av_log(NULL, AV_LOG_DEBUG, "video_store_concat avformat_free_context\n");
    avformat_free_context(outputCtx);
  }

  if (inputCtx != NULL) {
    av_log(NULL, AV_LOG_DEBUG, "video_store_concat avformat_close_input\n");
    avformat_close_input(&inputCtx);
  }

  if (options != NULL) {
    av_log(NULL, AV_LOG_DEBUG, "video_store_concat av_dict_free\n");
    av_dict_free(&options);
  }

  if (packet != NULL) {
    av_log(NULL, AV_LOG_DEBUG, "video_store_concat av_packet_free\n");
    av_packet_free(&packet);
  }

  return ret;
}

typedef void (*frame_callback_t)(uint8_t *data, int size, int stream_index, int64_t pts, void *user);

int video_store_emit_frames(const char *input_path, frame_callback_t cb, void *user) {
    AVFormatContext *fmt_ctx = NULL;
    AVPacket *pkt = NULL;
    int ret = -1;

    if ((ret = avformat_open_input(&fmt_ctx, input_path, NULL, NULL)) < 0) {
        av_log(NULL, AV_LOG_ERROR, "Failed to open input: %s\n", av_err2str(ret));
        return ret;
    }
    if ((ret = avformat_find_stream_info(fmt_ctx, NULL)) < 0) {
        av_log(NULL, AV_LOG_ERROR, "Failed to find stream info: %s\n", av_err2str(ret));
        goto cleanup;
    }

    pkt = av_packet_alloc();
    if (!pkt) {
        av_log(NULL, AV_LOG_ERROR, "Failed to allocate packet\n");
        ret = AVERROR(ENOMEM);
        goto cleanup;
    }

    while ((ret = av_read_frame(fmt_ctx, pkt)) >= 0) {
        // Emit only video frames (optional: check stream type)
        cb(pkt->data, pkt->size, pkt->stream_index, pkt->pts, user);
        av_packet_unref(pkt);
    }
    ret = 0; // Success

cleanup:
    if (pkt) av_packet_free(&pkt);
    if (fmt_ctx) avformat_close_input(&fmt_ctx);
    return ret == AVERROR_EOF ? 0 : ret;
}
