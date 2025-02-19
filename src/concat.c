#include "libavcodec/packet.h"
#include "libavutil/dict.h"
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <string.h>
#include "concat.h"

int video_store_concat(const char *concat_filepath, const char *output_path) {
  int ret = 0;
  AVPacket *packet = av_packet_alloc();
  AVDictionary *options = NULL;
  AVFormatContext *inputCtx = NULL;
  AVFormatContext *outputCtx = NULL;
  const AVInputFormat *inputFormat = av_find_input_format("concat");
  if (inputFormat == NULL) {
    goto error;
  }

  if (packet == NULL) {
    av_log(NULL, AV_LOG_ERROR, "video_store_concat av_packet_alloc failed\n");
    goto error;
  }

  ret = av_dict_set(&options, "safe", "0", 0);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to set option: %s\n",
           av_err2str(ret));
    goto error;
  }

  if ((ret = avformat_open_input(&inputCtx, concat_filepath, inputFormat,
                                 &options))) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to open input format: %s\n",
           av_err2str(ret));
    goto error;
  }

  ret = avformat_find_stream_info(inputCtx, NULL);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to find stream info: %s\n",
           av_err2str(ret));
    goto error;
  }

  ret = avformat_alloc_output_context2(&outputCtx, NULL, NULL, output_path);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to allocate output context: %s\n",
           av_err2str(ret));
    goto error;
  }

  for (unsigned int i = 0; i < inputCtx->nb_streams; i++) {
    AVStream *outStream = avformat_new_stream(outputCtx, NULL);
    if (outStream == NULL) {
      av_log(NULL, AV_LOG_ERROR,
             "video_store_concat failed to create ouput stream for input "
             "stream index %d, %s\n",
             i, av_err2str(ret));
      goto error;
    }

    ret = avcodec_parameters_copy(outStream->codecpar,
                                  inputCtx->streams[i]->codecpar);
    if (ret < 0) {
      av_log(NULL, AV_LOG_ERROR,
             "video_store_concat failed to copy input stream index %d codec "
             "parameters: %s",
             i, av_err2str(ret));
      goto error;
    }
    // Let ffmpeg handle the codec tag for us.
    outStream->codecpar->codec_tag = 0;
  }

  int outputPathOpened = 0;
  ret = avio_open(&outputCtx->pb, output_path, AVIO_FLAG_WRITE);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR,
           "video_store_concat failed to open output file: %s",
           av_err2str(ret));
    goto error;
  }
  outputPathOpened = 1;

  ret = avformat_write_header(outputCtx, NULL);
  if (ret < 0) {
    av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to write header: %s",
           av_err2str(ret));
    goto error;
  }

  int frame_ret = 0;
  AVStream *inStream = NULL;
  AVStream *outStream = NULL;
  while (1) {
    frame_ret = av_read_frame(inputCtx, packet);
    if (frame_ret == AVERROR_EOF) {
      break;
    };

    if (frame_ret) {
      ret = frame_ret;
      av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to read frame: %s",
             av_err2str(ret));
      goto error;
    }

    if ((packet->flags & AV_PKT_FLAG_DISCARD) == AV_PKT_FLAG_DISCARD) {
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
      av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to write frame: %s",
             av_err2str(ret));
      goto error;
    }
  }

  if ((ret = av_write_trailer(outputCtx))) {
    av_log(NULL, AV_LOG_ERROR, "video_store_concat failed to write trailer: %s",
           av_err2str(ret));
    goto error;
  }
  return VIDEO_STORE_CONCAT_RESP_OK;
error:
  if (outputCtx != NULL) {
    if (outputPathOpened) {
      int err = 0;
      if ((err = avio_closep(&outputCtx->pb))) {
        av_log(NULL, AV_LOG_ERROR,
               "video_store_concat failed to close output file: %s",
               av_err2str(err));
      };
    }
    avformat_free_context(outputCtx);
  }

  if (inputCtx != NULL) {
    avformat_close_input(&inputCtx);
  }

  if (options != NULL) {
    av_dict_free(&options);
  }

  if (packet != NULL) {
    av_packet_free(&packet);
  }

  // propagete ffmpeg error if ret < 0
  if (ret < 0) {
    return ret;
  }
  // otherwise propagate our own error
  return VIDEO_STORE_CONCAT_RESP_ERROR;
}
