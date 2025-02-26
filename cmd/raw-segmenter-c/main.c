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
    printf("CREATE TABLE extradata(id INTEGER NOT NULL PRIMARY KEY, data "
           "BLOB);\n");
    printf("CREATE TABLE packet(id INTEGER NOT NULL PRIMARY KEY, pts "
           "INTEGER,isIDR BOOLEAN, data BLOB);\n");

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
  if ((rc = sqlite3_prepare_v2(db, "SELECT data FROM extradata;", -1,
                               &statement, 0))) {
    printf("sqlite3_prepare failed: %d\n", rc);
    return rc;
  }

  rc = sqlite3_step(statement);
  if (rc != SQLITE_ROW) {
    printf("sqlite3_step failed: %d\n", rc);
    return rc;
  }

  ret = video_store_raw_seg_init_h265(
      &rs, 30, "./mp4s/%Y-%m-%d_%H-%M-%S.mp4",
      sqlite3_column_blob(statement, 0),
      (size_t)sqlite3_column_bytes(statement, 0), 2960, 1668);
  if (ret != VIDEO_STORE_RAW_SEG_RESP_OK) {
    printf("video_store_raw_seg_init_h265 failed: %d\n", ret);
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

  if ((rc = sqlite3_prepare_v2(db, "SELECT id, pts, isIDR, data FROM packet;",
                               -1, &statement, 0))) {
    printf("sqlite3_prepare failed: %d\n", rc);
    return rc;
  }
  int64_t pts = 0;
  int isIDR = 0;
  int failed = 0;
  while (1) {
    rc = sqlite3_step(statement);
    if (rc != SQLITE_ROW) {
      break;
    }
    pts = sqlite3_column_int64(statement, 1);
    isIDR = sqlite3_column_int(statement, 2);
    ret = video_store_raw_seg_write_h265_packet(
        rs, sqlite3_column_blob(statement, 3),
        (size_t)sqlite3_column_bytes(statement, 3), pts, isIDR);
    if (ret != VIDEO_STORE_RAW_SEG_RESP_OK) {
      failed = 1;
      printf("video_store_raw_seg_write_h265_packet failed: %d\n", ret);
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
