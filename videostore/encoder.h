#ifndef VIAM_ENCODER_H
#define VIAM_ENCODER_H
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/frame.h>
#include <libswscale/swscale.h>
#define MAX_PRESET_SIZE 30
#define MAX_OUTPUT_PATTERN_SIZE 1024
#define H264_CLOCK_TIME 90000
#define MICROSECONDS_IN_SECOND 1000000
typedef struct video_store_h264_encoder {
  // decoder
  AVCodecContext *decoderCtx;
  AVFrame *decoderFrame;

  // encoder
  AVCodecContext *encoderCtx;
  AVPacket *encoderPkt;
  int frameCount;

  // segmenter
  AVFormatContext *segmenterCtx;

  // satic config
  const AVCodec *encoderCodec;
  int segmentSeconds;
  const char *outputPattern;
  int64_t bitrate;
  int targetFrameRate;
  const char *preset;
} video_store_h264_encoder;

// video_store_h264_encoder_init initializes the encoder
int video_store_h264_encoder_init(struct video_store_h264_encoder **ppE, // OUT
                                  const int segmentSeconds,              // IN
                                  const char *outputPattern,             // IN
                                  const int64_t bitrate,                 // IN
                                  const int frameRate,                   // IN
                                  const char *preset                     // IN
);

// video_store_h264_encoder_write writes the payload frame to the encoder
// must be called at frame rate
// duplicate frames are allowed
int video_store_h264_encoder_write(struct video_store_h264_encoder *pE, // IN
                                   void *payload,                       // IN
                                   size_t payloadSize                   // IN
);

// video_store_h264_encoder_close stops and frees the encoder resources
int video_store_h264_encoder_close(struct video_store_h264_encoder **ppE // OUT
);
#define VIDEO_STORE_ENCODER_RESP_OK 0
#define VIDEO_STORE_ENCODER_RESP_ERROR 1
#endif /* VIAM_ENCODER_H */
