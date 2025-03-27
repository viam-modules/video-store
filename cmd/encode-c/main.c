#include "../../videostore/encoder.h"
#include <sqlite3.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(int argc, char *argv[]) {
  if (argc != 2 && argc != 3) {
    printf("usage: %s <sqlite.db> [short]\n", argv[0]);
    printf("TABLE should have the following schema:\n");
    printf("CREATE TABLE images(id INTEGER NOT NULL PRIMARY KEY, data BLOB, "
           "unixMicro INTEGER);\n");
    return 1;
  }

  av_log_set_level(AV_LOG_DEBUG);
  int bitrate = 1000000;
  int fps = 20;
  char preset[] = "ultrafast";
  struct video_store_h264_encoder *e = NULL;

  int ret = video_store_h264_encoder_init(
      &e, 30, "./mp4s/h264_%Y-%m-%d_%H-%M-%S.mp4", bitrate, fps, preset);
  if (ret != VIDEO_STORE_ENCODER_RESP_OK) {
    printf("Failed to init encoder: %d\n", ret);
    return 1;
  }
  ret = video_store_h264_encoder_close(&e);
  if (ret != VIDEO_STORE_ENCODER_RESP_OK) {
    printf("Failed to close encoder: %d\n", ret);
    return 1;
  }

  ret = video_store_h264_encoder_init(
      &e, 30, "./mp4s/h264_%Y-%m-%d_%H-%M-%S.mp4", bitrate, fps, preset);
  if (ret != VIDEO_STORE_ENCODER_RESP_OK) {
    printf("Failed to init encoder: %d\n", ret);
    return 1;
  }

  sqlite3 *db = NULL;
  int rc = 0;

  if ((rc = sqlite3_open(argv[1], &db))) {
    printf("Failed to open DB\n");
    return 1;
  };

  sqlite3_stmt *statement;
  printf("Performing query...\n");
  if (argc == 3) {
    if ((rc = sqlite3_prepare_v2(db,
                                 "SELECT data, unixMicro FROM images limit 60;",
                                 -1, &statement, 0))) {
      printf("sqlite3_prepare failed on images: %d\n", rc);
      return rc;
    }
  } else {
    if ((rc = sqlite3_prepare_v2(db, "SELECT data, unixMicro FROM images;", -1,
                                 &statement, 0))) {
      printf("sqlite3_prepare failed on images: %d\n", rc);
      return rc;
    }
  }

  int failed = 0;
  while (1) {
    rc = sqlite3_step(statement);
    if (rc != SQLITE_ROW) {
      break;
    }

    printf("calling video_store_h264_encoder_frame");
    ret = video_store_h264_encoder_write(
        e, (uint8_t *)sqlite3_column_blob(statement, 0),
        (size_t)sqlite3_column_bytes(statement, 0));
    if (ret != VIDEO_STORE_ENCODER_RESP_OK) {
      printf("Failed to write frame: %d\n", ret);
      failed = 1;
      break;
    }
  }

  ret = video_store_h264_encoder_close(&e);
  if (ret != VIDEO_STORE_ENCODER_RESP_OK) {
    printf("Failed to close encoder: %d\n", ret);
    return 1;
  }
  rc = sqlite3_finalize(statement);
  if (rc != SQLITE_OK) {
    printf("sqlite3_finalize failed: %d\n", rc);
    return rc;
  }
  rc = sqlite3_close(db);
  if (rc != SQLITE_OK) {
    printf("sqlite3_close failed: %d\n", rc);
    return rc;
  }

  if (failed) {
    printf("failed: %d\n", failed);
    return 1;
  }
  printf("succeeded\n");
  return 0;
}
