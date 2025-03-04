#include "../../videostore/rawsegmenter.h"
#include "libavutil/log.h"
#include <sqlite3.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(int argc, char *argv[]) {
  if (argc != 2) {
    printf("usage: %s <sqlite.db>\n", argv[0]);
    printf("TABLE should have the following schema:\n");

    printf("CREATE TABLE extradata(id INTEGER NOT NULL PRIMARY KEY, isH264 "
           "BOOLEAN, width INTEGER, height INTEGER);\n");
    printf("CREATE TABLE packet(id INTEGER NOT NULL PRIMARY KEY, pts "
           "INTEGER,dts INTEGER,isIDR BOOLEAN, data BLOB);\n");

    return 1;
  }

  av_log_set_level(AV_LOG_DEBUG);
  sqlite3 *db = NULL;
  int rc = 0;

  if ((rc = sqlite3_open(argv[1], &db))) {
    printf("Failed to open DB\n");
    return 1;
  };

  struct raw_seg *rs;
  int ret = 0;
  sqlite3_stmt *statement;
  /* Compile the SELECT statement into a virtual machine. */
  printf("Performing query...\n");
  if ((rc = sqlite3_prepare_v2(db,
                               "SELECT isH264, width, height FROM extradata;",
                               -1, &statement, 0))) {
    printf("sqlite3_prepare failed on extradata: %d\n", rc);
    return rc;
  }

  rc = sqlite3_step(statement);
  if (rc != SQLITE_ROW) {
    printf("sqlite3_step failed: %d\n", rc);
    return rc;
  }
  int isH264 = sqlite3_column_int(statement, 0);
  int width = sqlite3_column_int(statement, 1);
  int height = sqlite3_column_int(statement, 2);

  if (isH264) {
    ret = video_store_raw_seg_init_h264(
        &rs, 30, "./mp4s/h264_%Y-%m-%d_%H-%M-%S.mp4", width, height);
  } else {
    ret = video_store_raw_seg_init_h265(
        &rs, 30, "./mp4s/h265_%Y-%m-%d_%H-%M-%S.mp4", width, height);
  }
  if (ret != VIDEO_STORE_RAW_SEG_RESP_OK) {
    printf("video_store_raw_seg_init failed: %d\n", ret);
    rc = sqlite3_finalize(statement);
    if (rc != SQLITE_OK) {
      printf("sqlite3_finalize failed: %d\n", rc);
    }
    rc = sqlite3_close(db);
    if (rc != SQLITE_OK) {
      printf("sqlite3_finalize failed: %d\n", rc);
    }
    return ret;
  }
  rc = sqlite3_finalize(statement);
  if (rc != SQLITE_OK) {
    printf("sqlite3_finalize failed: %d\n", rc);
    return rc;
  }

  if ((rc = sqlite3_prepare_v2(db,
                               "SELECT id, pts, dts, isIDR, data FROM packet;",
                               -1, &statement, 0))) {
    printf("sqlite3_prepare failed: %d\n", rc);
    return rc;
  }
  int64_t pts = 0;
  int64_t dts = 0;
  int isIDR = 0;
  int failed = 0;
  while (1) {
    rc = sqlite3_step(statement);
    if (rc != SQLITE_ROW) {
      break;
    }
    pts = sqlite3_column_int64(statement, 1);
    dts = sqlite3_column_int64(statement, 2);
    isIDR = sqlite3_column_int(statement, 3);
    ret = video_store_raw_seg_write_packet(
        rs, sqlite3_column_blob(statement, 4),
        (size_t)sqlite3_column_bytes(statement, 4), pts, dts, isIDR);
    if (ret != VIDEO_STORE_RAW_SEG_RESP_OK) {
      failed = 1;
      printf("video_store_raw_seg_write_packet failed: %d\n", ret);
      break;
    }
  }

  ret = video_store_raw_seg_close(&rs);
  if (ret != VIDEO_STORE_RAW_SEG_RESP_OK) {
    printf("video_store_raw_seg_close failed: %d\n", ret);
    return ret;
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
