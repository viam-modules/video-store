#ifndef VIAM_ENCODER_H
#define VIAM_ENCODER_H
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/frame.h>
#include <libswscale/swscale.h>
typedef struct video_store_h264_encoder {
  AVFormatContext *segmenterCtx;
  AVStream *segmenterStream;

  AVCodecContext *decoderCtx;
  AVFrame *decoderFrame;
  int frameCount;

  AVFrame *encoderFrame;
  AVCodecContext *encoderCtx;
} viam_encoder;

int video_store_h264_encoder_init(struct video_store_h264_encoder **ppE, // OUT
                                  const int segmentSeconds,              // IN
                                  const char *outputPattern,             // IN
                                  const int width,                       // IN
                                  const int height,                      // IN
                                  const int64_t bitrate, const int frameRate,
                                  const char *preset);

int video_store_h264_encoder_frame(struct video_store_h264_encoder *rs, // IN
                                   uint8_t *payload,                    // IN
                                   int payloadSize                      // IN
);

int video_store_h264_encoder_close(struct video_store_h264_encoder **ppE // OUT
);
#define VIDEO_STORE_ENCODER_RESP_OK 0
#define VIDEO_STORE_ENCODER_RESP_ERROR 1
#endif /* VIAM_ENCODER_H */
