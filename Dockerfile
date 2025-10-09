FROM debian:bookworm-slim AS build

WORKDIR /build

# Install dependencies
RUN apt-get update && apt-get install -y \
    build-essential \
    git \
    pkg-config \
    cmake \
    libtool \
    ca-certificates \
    liblmdb-dev \
    libssl-dev \
    zlib1g-dev \
    libsecp256k1-dev \
    libzstd-dev \
    nlohmann-json3-dev \
    && rm -rf /var/lib/apt/lists/*

# Build FlatBuffers from source
RUN git clone --branch v25.1.21 --depth 1 https://github.com/google/flatbuffers.git /tmp/flatbuffers \
  && cd /tmp/flatbuffers \
  && cmake -G "Unix Makefiles" -DCMAKE_BUILD_TYPE=Release \
  && make -j$(nproc) \
  && make install \
  && ldconfig \
  && cd / \
  && rm -rf /tmp/flatbuffers

# Copy source
COPY . .

# Build - delete build dir to force schema regeneration
RUN git submodule update --init \
  && make setup-golpe \
  && rm -rf build/ \
  && cd golpe/external/uWebSockets && make -j && cd ../../.. \
  && make -j2

# Runtime stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    liblmdb0 \
    libssl3 \
    zlib1g \
    libsecp256k1-1 \
    libzstd1 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /build/strfry /usr/local/bin/
COPY --from=build /usr/local/lib/libflatbuffers* /usr/local/lib/
RUN ldconfig

WORKDIR /app

EXPOSE 7777 8080

ENTRYPOINT ["/usr/local/bin/strfry"]
CMD ["relay"]