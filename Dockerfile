# syntax=docker/dockerfile:1.10

ARG DEBIAN=debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2
ARG GOLANG=golang:1.27.3-trixie@sha256:af1bfc2614ebb1dbd31af514b78740fd678850e76498762b425ecee4c5c6ea80

# whisper.cpp, built for each platform on a native runner (C++ under QEMU takes far too long).
FROM ${DEBIAN} AS whisper
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential cmake \
    && rm -rf /var/lib/apt/lists/*
ADD --checksum=sha256:ff1a9053feb509ff9d7729703355541ae9690073a6b1c40eb692c962e0dc1720 \
    https://github.com/ggml-org/whisper.cpp/archive/refs/tags/v1.9.5.tar.gz /whisper.tar.gz
ARG TARGETARCH
# GGML_NATIVE would tune for the build runner's CPU. Instead target the cluster: x86-64-v3 for
# kube5, and armv8.2-a with dotprod and fp16 for the RK3588-class arm64 nodes.
RUN mkdir /src && tar -xzf /whisper.tar.gz -C /src --strip-components=1 \
    && case "$TARGETARCH" in \
         amd64) arch="-DGGML_AVX=ON -DGGML_AVX2=ON -DGGML_FMA=ON -DGGML_F16C=ON -DGGML_BMI2=ON" ;; \
         arm64) arch="-DGGML_CPU_ARM_ARCH=armv8.2-a+dotprod+fp16" ;; \
         *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;; \
       esac \
    && cmake -S /src -B /build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
         -DWHISPER_BUILD_EXAMPLES=OFF -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_SERVER=OFF \
         -DGGML_NATIVE=OFF $arch \
    && cmake --build /build -j"$(nproc)" \
    && mkdir -p /out/lib /out/include \
    && find /build -name '*.a' -exec cp {} /out/lib/ \; \
    && cp /src/include/*.h /src/ggml/include/*.h /out/include/

FROM ${GOLANG} AS build
COPY --from=whisper /out /whisper
ENV CGO_ENABLED=1 C_INCLUDE_PATH=/whisper/include LIBRARY_PATH=/whisper/lib
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/reel-to-tandoor ./cmd/reel-to-tandoor

FROM build AS test
RUN test -z "$(gofmt -l .)" && go vet ./... && go test ./...

# yt-dlp's standalone builds bundle their own Python. The onedir build is unpacked here; the
# one-file build unpacks itself into /tmp on every run, which fails when /tmp is noexec.
FROM scratch AS ytdlp-amd64
ADD --checksum=sha256:f34053415136e55073dd64ba5326d161ecc23ce0e98c081d8752867b37809450 \
    https://github.com/yt-dlp/yt-dlp/releases/download/2026.10.26/yt-dlp_linux.zip /yt-dlp.zip
FROM scratch AS ytdlp-arm64
ADD --checksum=sha256:74708e707e8fa8177c8347e58152337edf320149cd7b225defd453f7cde47822 \
    https://github.com/yt-dlp/yt-dlp/releases/download/2026.10.26/yt-dlp_linux_aarch64.zip /yt-dlp.zip
FROM ytdlp-${TARGETARCH} AS ytdlp-zip
FROM ${DEBIAN} AS ytdlp
RUN apt-get update \
    && apt-get install -y --no-install-recommends unzip \
    && rm -rf /var/lib/apt/lists/*
COPY --from=ytdlp-zip /yt-dlp.zip /tmp/
RUN unzip -q /tmp/yt-dlp.zip -d /opt/yt-dlp \
    && mv /opt/yt-dlp/yt-dlp_linux* /opt/yt-dlp/yt-dlp \
    && chmod -R a+rX /opt/yt-dlp

FROM scratch AS models
# q8_0 transcribed the test reel word for word like the full model, in half the space.
ADD --chmod=644 --checksum=sha256:49c8fb02b65e6049d5fa6c04f81f53b867b5ec9540406812c643f177317f779f \
    https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/ggml-small-q8_0.bin /models/
ADD --chmod=644 --checksum=sha256:2aa269b785eeb53a82983a20501ddf7c1d9c48e33ab63a41391ac6c9f7fb6987 \
    https://huggingface.co/ggml-org/whisper-vad/resolve/9ffd54a1e1ee413ddf265af9913beaf518d1639b/ggml-silero-v6.2.0.bin /models/

FROM ${DEBIAN}
LABEL org.opencontainers.image.source="https://github.com/1Solon/reel-to-tandoor"
LABEL org.opencontainers.image.description="Save an Instagram reel's recipe into Tandoor"
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libgomp1 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=mwader/static-ffmpeg:9.0.2-1@sha256:f5d100184884eb3e82b0eb14bb5ebf98da99826ac824989720297fb9ef21c822 /ffmpeg /usr/local/bin/ffmpeg
COPY --from=ytdlp /opt/yt-dlp /opt/yt-dlp
COPY --from=models /models /models
COPY --from=build /out/reel-to-tandoor /usr/local/bin/reel-to-tandoor
ENV YTDLP_PATH=/opt/yt-dlp/yt-dlp \
    WHISPER_MODEL=/models/ggml-small-q8_0.bin \
    WHISPER_VAD_MODEL=/models/ggml-silero-v6.2.0.bin
USER 1000:1000
EXPOSE 8080
CMD ["reel-to-tandoor"]
