# syntax=docker/dockerfile:1

# The whisper model is the same on every platform, so fetch it once on the build machine.
FROM --platform=$BUILDPLATFORM python:3.13-slim-trixie@sha256:09a17cad02668ae61ece136b4fd36ac3849e14f670ebedb637ea3e15580525bc AS model
ARG WHISPER_REPO=Systran/faster-whisper-small
ARG WHISPER_REVISION=536b0662742c02347bc0e980a01041f333bce120
RUN pip install --no-cache-dir "huggingface_hub>=1,<2" \
    && hf download "$WHISPER_REPO" --revision "$WHISPER_REVISION" --local-dir /models/whisper \
        config.json model.bin tokenizer.json vocabulary.txt \
    && rm -rf /models/whisper/.cache

FROM python:3.13-slim-trixie@sha256:09a17cad02668ae61ece136b4fd36ac3849e14f670ebedb637ea3e15580525bc AS build
COPY --from=ghcr.io/astral-sh/uv:0.13.7@sha256:96f40d3904aad8d0bd010539001d36c8e2188b981e1334471a90c4a1d93bb490 /uv /bin/uv
ENV UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy UV_PYTHON_DOWNLOADS=never
WORKDIR /app
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project
COPY src ./src
RUN uv sync --frozen --no-dev --no-editable

FROM python:3.13-slim-trixie@sha256:09a17cad02668ae61ece136b4fd36ac3849e14f670ebedb637ea3e15580525bc
LABEL org.opencontainers.image.source="https://github.com/1Solon/reel-to-tandoor"
LABEL org.opencontainers.image.description="Save an Instagram reel's recipe into Tandoor"
COPY --from=build /app/.venv /app/.venv
COPY --from=model /models /models
ENV PATH=/app/.venv/bin:$PATH \
    PYTHONUNBUFFERED=1 \
    HF_HUB_OFFLINE=1 \
    WHISPER_MODEL=/models/whisper \
    XDG_CACHE_HOME=/tmp/.cache
USER 1000:1000
EXPOSE 8080
CMD ["reel-to-tandoor"]
