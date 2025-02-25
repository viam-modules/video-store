#include "concat.h"
#include "libavcodec/packet.h"
#include "libavutil/dict.h"
#include "libavutil/log.h"
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <string.h>

int video_store_concat(const char *concat_filepath, const char *output_path) {
  int ret = VIDEO_STORE_CONCAT_RESP_ERROR;
  AVPacket *packet = av_packet_alloc();
  AVDictionary *options = NULL;
  AVFormatContext *inputCtx = NULL;
  AVFormatContext *outputCtx = NULL;
  int outputPathOpened = 0;
  const AVInputFormat *inputFormat = av_find_input_format("concat");
  if (inputFormat == NULL) {
    av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to find input format\n");
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

  ret = avformat_alloc_output_context2(&outputCtx, NULL, NULL, output_path);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to allocate output context: %s\n",
           av_err2str(ret));
    goto cleanup;
  }

  // Store codec parameters of the first input stream for comparison
  AVCodecParameters *firstCodecPar = inputCtx->streams[0]->codecpar;

  for (unsigned int i = 0; i < inputCtx->nb_streams; i++) {
    AVStream *inStream = inputCtx->streams[i];
    AVStream *outStream = avformat_new_stream(outputCtx, NULL);
    if (outStream == NULL) {
      av_log(NULL, AV_LOG_ERROR,
             "video_store_concat failed to create output stream for input "
             "stream index %d, %s\n",
             i, av_err2str(ret));
      goto cleanup;
    }

    // Check for codec parameter mismatches
    if (inStream->codecpar->codec_id != firstCodecPar->codec_id ||
        inStream->codecpar->width != firstCodecPar->width ||
        inStream->codecpar->height != firstCodecPar->height) {
      av_log(NULL, AV_LOG_ERROR,
             "video_store_concat codec parameter mismatch for input stream index %d\n",
             i);
      ret = AVERROR(EINVAL);
      goto write_trailer;
    }

    ret = avcodec_parameters_copy(outStream->codecpar, inStream->codecpar);
    if (ret < 0) {
      av_log(NULL, AV_LOG_ERROR,
             "video_store_concat failed to copy input stream index %d codec "
             "parameters: %s",
             i, av_err2str(ret));
      goto cleanup;
    }
    // Let ffmpeg handle the codec tag for us.
    outStream->codecpar->codec_tag = 0;
  }

  ret = avio_open(&outputCtx->pb, output_path, AVIO_FLAG_WRITE);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to open output file: %s",
           av_err2str(ret));
    goto cleanup;
  }
  outputPathOpened = 1;

  ret = avformat_write_header(outputCtx, NULL);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to write header: %s",
           av_err2str(ret));
    goto cleanup;
  }

  int frame_ret = 0;
  AVStream *inStream = NULL;
  AVStream *outStream = NULL;
  while (1) {
    frame_ret = av_read_frame(inputCtx, packet);
    if (frame_ret == AVERROR_EOF) {
      av_packet_unref(packet);
      break;
    };

    if (frame_ret) {
      ret = frame_ret;
      av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to read frame: %s",
             av_err2str(ret));
      av_packet_unref(packet);
      goto write_trailer;
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

    if ((ret = av_interleaved_write_frame(outputCtx, packet))) {
      av_packet_unref(packet);
      av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to write frame: %s",
             av_err2str(ret));
      goto write_trailer;
    }
    av_packet_unref(packet);
  }

write_trailer:
  if ((ret = av_write_trailer(outputCtx))) {
    av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to write trailer: %s",
           av_err2str(ret));
  } else {
    ret = VIDEO_STORE_CONCAT_RESP_OK;
  }

cleanup:
  av_log(NULL, AV_LOG_DEBUG, "video_store_concat going to cleanup\n");
  if (outputCtx != NULL) {
    if (outputPathOpened) {
      int err = 0;
      if ((err = avio_closep(&outputCtx->pb))) {
        av_log(NULL, AV_LOG_ERROR,
               "video_store_concat failed to close output file: %s",
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
