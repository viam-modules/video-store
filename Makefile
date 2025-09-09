SOURCE_OS ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
SOURCE_ARCH ?= $(shell uname -m)
TARGET_OS ?= $(SOURCE_OS)
TARGET_ARCH ?= $(SOURCE_ARCH)
normalize_arch = $(if $(filter aarch64,$(1)),arm64,$(if $(filter x86_64,$(1)),amd64,$(1)))
SOURCE_ARCH := $(call normalize_arch,$(SOURCE_ARCH))
TARGET_ARCH := $(call normalize_arch,$(TARGET_ARCH))

BIN_OUTPUT_PATH = bin/$(TARGET_OS)-$(TARGET_ARCH)
ifeq ($(TARGET_OS),windows)
	BIN_SUFFIX := .exe
endif
BIN_VIDEO_STORE := $(BIN_OUTPUT_PATH)/video-store$(BIN_SUFFIX)
BIN_CONCAT := $(BIN_OUTPUT_PATH)/concat$(BIN_SUFFIX)
BIN_CONCAT_C := $(BIN_OUTPUT_PATH)/concat-c$(BIN_SUFFIX)
BIN_ENCODER_C := $(BIN_OUTPUT_PATH)/encoder-c$(BIN_SUFFIX)
BIN_RAW_SEGMENTER_C := $(BIN_OUTPUT_PATH)/raw-segmenter-c$(BIN_SUFFIX)
BIN_VIDEO_INFO_C := $(BIN_OUTPUT_PATH)/video-info-c$(BIN_SUFFIX)
TOOL_BIN = bin/gotools/$(shell uname -s)-$(shell uname -m)

ifeq ($(SOURCE_OS),linux)
    NPROC ?= $(shell nproc)
else ifeq ($(SOURCE_OS),darwin)
    NPROC ?= $(shell sysctl -n hw.ncpu)
else
    NPROC ?= 1
endif

FFMPEG_TAG ?= n6.1
FFMPEG_VERSION ?= $(shell pwd)/FFmpeg/$(FFMPEG_TAG)
FFMPEG_VERSION_PLATFORM ?= $(FFMPEG_VERSION)/$(TARGET_OS)-$(TARGET_ARCH)
FFMPEG_BUILD ?= $(FFMPEG_VERSION_PLATFORM)/build
FFMPEG_LIBS=    libavformat                        \
                libavcodec                         \
                libavutil                          \
                libswscale                         \

FFMPEG_OPTS ?= --prefix=$(FFMPEG_BUILD) \
               --disable-shared \
               --disable-programs \
               --disable-doc \
               --disable-everything \
               --enable-static \
               --enable-libx264 \
               --enable-decoder=hevc \
               --enable-decoder=h264 \
               --enable-gpl \
               --enable-encoder=libx264 \
               --enable-muxer=segment \
               --enable-muxer=mp4 \
               --enable-demuxer=segment \
               --enable-demuxer=concat \
               --enable-demuxer=mov \
               --enable-demuxer=mp4 \
               --enable-parser=h264 \
               --enable-parser=hevc \
               --enable-protocol=file \
               --enable-protocol=concat \
               --enable-protocol=crypto \
               --enable-bsf=h264_mp4toannexb \
               --enable-bsf=hevc_mp4toannexb \
               --enable-decoder=mjpeg

GOFLAGS := -buildvcs=false
SRC_DIR := videostore
BUILD_DIR := build/$(TARGET_OS)-$(TARGET_ARCH)
SRCS := $(shell find $(SRC_DIR) -name '*.c')
OBJS := $(subst $(SRC_DIR), $(BUILD_DIR), $(SRCS:.c=.o))
PKG_CONFIG_PATH = $(FFMPEG_BUILD)/lib/pkgconfig
CGO_CFLAGS = $(shell PKG_CONFIG_PATH=$(PKG_CONFIG_PATH) pkg-config --cflags $(FFMPEG_LIBS))
ifeq ($(SOURCE_OS),linux)
	SUBST = -l:libx264.a
endif
ifeq ($(SOURCE_OS),darwin)
	SUBST = $(HOMEBREW_PREFIX)/Cellar/x264/r3222/lib/libx264.a
endif
CGO_LDFLAGS = $(subst -lx264, $(SUBST),$(shell PKG_CONFIG_PATH=$(PKG_CONFIG_PATH) pkg-config --libs $(FFMPEG_LIBS)))
ifeq ($(TARGET_OS),windows)
	CGO_LDFLAGS += -static -static-libgcc -static-libstdc++
endif
export PATH := $(PATH):$(shell go env GOPATH)/bin
ifeq ($(TARGET_OS),windows)
ifeq ($(SOURCE_OS),linux)
ifeq ($(TARGET_ARCH),amd64)
    GO_TAGS ?= -tags no_cgo
    X264_ROOT ?= $(shell pwd)/x264/windows-amd64
    X264_BUILD_DIR ?= $(X264_ROOT)/build
    # We need the go build command to think it's in cgo mode
    export CGO_ENABLED = 1
    # mingw32 flags refer to 64 bit windows target
    export CC=/usr/bin/x86_64-w64-mingw32-gcc
    export CXX=/usr/bin/x86_64-w64-mingw32-g++
    export AS=x86_64-w64-mingw32-as
    export AR=x86_64-w64-mingw32-ar
    export RANLIB=x86_64-w64-mingw32-ranlib
    export LD=x86_64-w64-mingw32-ld
    export STRIP=x86_64-w64-mingw32-strip
    FFMPEG_OPTS += --target-os=mingw32 \
                   --arch=x86 \
                   --cpu=x86-64 \
                   --cross-prefix=x86_64-w64-mingw32- \
                   --enable-cross-compile \
                   --pkg-config=$(shell pwd)/etc/pkg-config-wrapper.sh
endif
endif
endif
.PHONY: lint tool-install test clean clean-all clean-ffmpeg module build valgrind valgrind-run

all: $(FFMPEG_BUILD) $(BIN_VIDEO_STORE) $(BIN_CONCAT)

$(BIN_VIDEO_STORE): videostore/*.go cmd/module/*.go videostore/*.c videostore/utils/*.c videostore/*.h videostore/utils/*.h $(FFMPEG_BUILD)
	go mod tidy
	GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CFLAGS="$(CGO_CFLAGS)" go build -o $(BIN_VIDEO_STORE) cmd/module/cmd.go

$(BIN_CONCAT): videostore/*.go cmd/concat/*.go videostore/*.c videostore/*.h $(FFMPEG_BUILD)
	go mod tidy
	GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CFLAGS="$(CGO_CFLAGS)" go build -o $(BIN_CONCAT) cmd/concat/cmd.go

AR = ar
$(BUILD_DIR)/libviamav.a:
	$(AR) crs $@ $(BUILD_DIR)/concat.o $(BUILD_DIR)/rawsegmenter.o

$(BIN_CONCAT_C): $(FFMPEG_BUILD) $(OBJS) $(BUILD_DIR)/libviamav.a | $(BUILD_DIR) $(BIN_OUTPUT_PATH)
	@echo "-------- Make $(BIN_OUTPUT_PATH)/concat-c --------"
	rm -f $(BIN_CONCAT_C)
	mkdir -p $(BUILD_DIR)
	$(CC) $(OBJS) ./cmd/concat-c/main.c $(CGO_LDFLAGS) -ldl -L$(BUILD_DIR) -lviamav $(CGO_CFLAGS)  -g -o $(BIN_CONCAT_C)

$(BIN_ENCODER_C): $(FFMPEG_BUILD) $(OBJS) $(BUILD_DIR)/libviamav.a | $(BUILD_DIR) $(BIN_OUTPUT_PATH)
	@echo "-------- Make $(BIN_OUTPUT_PATH)/concat-c --------"
	rm -f $(BIN_ENCODER_C)
	mkdir -p $(BUILD_DIR)
	$(CC) $(OBJS) ./cmd/encode-c/main.c $(CGO_LDFLAGS) $(shell pkg-config --cflags sqlite3) -ldl -L$(BUILD_DIR) -lviamav $(CGO_CFLAGS)  $(shell pkg-config --libs sqlite3) -g -o $(BIN_ENCODER_C)

$(BIN_RAW_SEGMENTER_C): $(FFMPEG_BUILD) $(OBJS) $(BUILD_DIR)/libviamav.a | $(BUILD_DIR) $(BIN_OUTPUT_PATH)
	@echo "-------- Make $(BIN_OUTPUT_PATH)/concat-c --------"
	rm -f $(BIN_RAW_SEGMENTER_C)
	mkdir -p $(BUILD_DIR)
	$(CC) $(OBJS) ./cmd/raw-segmenter-c/main.c $(CGO_LDFLAGS) $(shell pkg-config --cflags sqlite3) -ldl -L$(BUILD_DIR) -lviamav $(CGO_CFLAGS)  $(shell pkg-config --libs sqlite3) -g -o $(BIN_RAW_SEGMENTER_C)

$(BIN_VIDEO_INFO_C): $(FFMPEG_BUILD) $(OBJS) | $(BUILD_DIR) $(BIN_OUTPUT_PATH)
	@echo "-------- Make $(BIN_OUTPUT_PATH)/video-info-c --------"
	rm -f $(BIN_VIDEO_INFO_C)
	mkdir -p $(BUILD_DIR)
	$(CC) $(OBJS) ./cmd/video-info-c/main.c $(CGO_LDFLAGS) -ldl $(CGO_CFLAGS) -g -o $(BIN_VIDEO_INFO_C)

$(BUILD_DIR)/%.o: $(SRC_DIR)/%.c | $(BUILD_DIR)
	@echo "-------- Make $(@) --------"
	rm -f $@
	$(CC) $(CGO_LDFLAGS) $(CGO_CFLAGS) -g -c -o $@ $<

$(BUILD_DIR):
	@echo "-------- mkdir $(@) --------"
	mkdir -p $(BUILD_DIR)

$(BIN_OUTPUT_PATH):
	@echo "-------- mkdir $(@) --------"
	mkdir -p $(BIN_OUTPUT_PATH)

$(FFMPEG_VERSION_PLATFORM):
	git clone https://github.com/FFmpeg/FFmpeg.git --depth 1 --branch $(FFMPEG_TAG) $(FFMPEG_VERSION_PLATFORM)

$(FFMPEG_BUILD): $(FFMPEG_VERSION_PLATFORM) $(X264_BUILD_DIR)
# Only need nasm to build assembly kernels for amd64 targets.
ifeq ($(SOURCE_OS),linux)
ifeq ($(TARGET_OS),linux)
ifeq ($(shell dpkg -l | grep -w x264 > /dev/null; echo $$?), 1)
	sudo apt update && sudo apt install -y libx264-dev
endif
endif
ifeq ($(SOURCE_ARCH),amd64)
	which nasm || (sudo apt update && sudo apt install -y nasm)
endif
endif
ifeq ($(SOURCE_OS),darwin)
ifeq ($(shell brew list | grep -w x264 > /dev/null; echo $$?), 1)
	brew update && brew install x264
endif
endif
	cd $(FFMPEG_VERSION_PLATFORM) && ./configure $(FFMPEG_OPTS) && $(MAKE) -j$(NPROC) && $(MAKE) install

tool-install:
	GOBIN=`pwd`/$(TOOL_BIN) go install \
		github.com/edaniels/golinters/cmd/combined \
		github.com/golangci/golangci-lint/cmd/golangci-lint \
		github.com/rhysd/actionlint/cmd/actionlint

lint: tool-install $(FFMPEG_BUILD)
	go mod tidy
	CGO_CFLAGS=$(CGO_CFLAGS) GOFLAGS=$(GOFLAGS) $(TOOL_BIN)/golangci-lint run -v --fix --config=./etc/.golangci.yaml --timeout=2m

test: $(BIN_VIDEO_STORE)
ifeq ($(shell which ffmpeg > /dev/null 2>&1; echo $$?), 1)
ifeq ($(SOURCE_OS),linux)
	sudo apt update && sudo apt install -y ffmpeg
endif
ifeq ($(SOURCE_OS),darwin)
	brew update && brew install ffmpeg
endif
endif
ifeq ($(shell which artifact > /dev/null 2>&1; echo $$?), 1)
	go install go.viam.com/utils/artifact/cmd/artifact@latest
endif
	artifact pull
	cp $(BIN_VIDEO_STORE) bin/video-store$(BIN_SUFFIX)
	CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CFLAGS=$(CGO_CFLAGS) go test -v ./...
	rm bin/video-store

module: $(BIN_VIDEO_STORE)
	cp $(BIN_VIDEO_STORE) bin/video-store$(BIN_SUFFIX)
	tar czf module.tar.gz bin/video-store$(BIN_SUFFIX)
	rm bin/video-store$(BIN_SUFFIX)

update-rdk:
	go get go.viam.com/rdk@latest
	go mod tidy

clean:
	rm -rf bin
	rm -rf build

clean-ffmpeg: clean
	rm -rf FFmpeg

clean-all: clean clean-ffmpeg
	git clean -fxd

valgrind-setup: 
ifeq ($(shell which valgrind > /dev/null 2>&1; echo $$?), 1)
ifeq ($(SOURCE_OS),linux)
	wget https://sourceware.org/pub/valgrind/valgrind-3.24.0.tar.bz2
	tar xjf valgrind-3.24.0.tar.bz2
	rm -rf valgrind-3.24.0.tar.bz2
	cd valgrind-3.24.0
	cd valgrind-3.24.0 && ./configure && make && sudo make install
	rm -rf valgrind-3.24.0.tar.bz2
	rm -rf valgrind-3.24.0
endif
ifeq ($(SOURCE_OS),darwin)
	echo "valgrind not supported on macos building in canon"
	canon make valgrind-setup
endif
endif

# you need latest valgrind, otherwise you might not get line numbers in your valgrind output
valgrind-run: TARGET=$(BIN_OUTPUT_PATH)/$(PROGRAM)
valgrind-run: 
ifeq ($(SOURCE_OS),linux)
	sudo apt-get install libc6-dbg
	valgrind --error-exitcode=1 --leak-check=full --track-origins=yes --dsymutil=yes -v $(TARGET) $(ARGS)
endif
ifeq ($(SOURCE_OS),darwin)
	echo "valgrind not supported on macos running in canon"
	canon make clean ./bin/linux-arm64/raw-segmenter-c valgrind-run
endif

run:
ifeq ($(SOURCE_OS),linux)
	./bin/linux-arm64/raw-segmenter-c my.db
endif
ifeq ($(SOURCE_OS),darwin)
	echo "valgrind not supported on macos running in canon"
	canon make clean ./bin/linux-arm64/raw-segmenter-c run
endif

gdb-run: 
ifeq ($(SOURCE_OS),linux)
	gdb ./bin/linux-arm64/raw-segmenter-c 
endif
ifeq ($(SOURCE_OS),darwin)
	echo "valgrind not supported on macos running in canon"
	canon make clean ./bin/linux-arm64/raw-segmenter-c gdb-run
endif

$(X264_ROOT):
ifeq ($(TARGET_OS),windows)
ifeq ($(SOURCE_OS),linux)
ifeq ($(TARGET_ARCH),amd64)
	git clone https://code.videolan.org/videolan/x264.git $(X264_ROOT)
endif
endif
endif

$(X264_BUILD_DIR): $(X264_ROOT)
ifeq ($(TARGET_OS),windows)
ifeq ($(SOURCE_OS),linux)
ifeq ($(TARGET_ARCH),amd64)
ifeq ($(shell which x86_64-w64-mingw32-gcc > /dev/null; echo $$?), 1)
	$(info MinGW cross compiler not found, installing...)
	sudo apt-get update && sudo apt-get install -y mingw-w64
endif
	cd $(X264_ROOT) && \
	./configure \
		--host=x86_64-w64-mingw32 \
		--cross-prefix=x86_64-w64-mingw32- \
		--prefix=$(X264_BUILD_DIR) \
		--enable-static --disable-opencl \
		--disable-asm && \
	make -j$(NPROC) && \
	make install
endif
endif
endif
